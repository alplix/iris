package scheduler

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ResultUpload describes one task output file to hand to a project's file
// upload handler. Name is the file's physical name (what the project's
// certificate was signed for), Path where it sits on disk.
type ResultUpload struct {
	Name      string
	Path      string
	URLs      []string
	MaxNBytes float64
	Signature string
}

// UploadError is a failed upload. Permanent means the server will never take
// this file (bad signature, too large) so retrying is pointless.
type UploadError struct {
	Msg       string
	Permanent bool
}

func (e *UploadError) Error() string { return e.Msg }

// IsPermanent lets callers that do not import this package ask the question.
func (e *UploadError) IsPermanent() bool { return e.Permanent }

// FileDigest returns a file's size and MD5, both of which go into the report.
func FileDigest(path string) (size int64, sum string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := md5.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// UploadResultFile sends an output file with the same request the reference
// client makes (client/file_xfer.cpp): first a <get_file_size> query so an
// interrupted upload resumes where it stopped, then a <file_upload> request
// whose header carries the project's signed certificate followed by the raw
// bytes. Every listed URL is tried in turn.
func UploadResultFile(ctx context.Context, u ResultUpload, clientVersion string, progress ProgressFunc) error {
	if len(u.URLs) == 0 {
		return &UploadError{Msg: "the project gave no upload address for " + u.Name, Permanent: true}
	}
	size, sum, err := FileDigest(u.Path)
	if err != nil {
		return &UploadError{Msg: fmt.Sprintf("read %s: %v", u.Path, err), Permanent: true}
	}
	var last error
	for _, target := range u.URLs {
		err := uploadTo(ctx, target, u, size, sum, clientVersion, progress)
		if err == nil {
			return nil
		}
		last = err
		if ue, ok := err.(*UploadError); ok && ue.Permanent {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return last
}

func versionTags(clientVersion string) string {
	major, minor, rel := 8, 0, 2
	fmt.Sscanf(clientVersion, "%d.%d.%d", &major, &minor, &rel)
	return fmt.Sprintf("    <core_client_major_version>%d</core_client_major_version>\n"+
		"    <core_client_minor_version>%d</core_client_minor_version>\n"+
		"    <core_client_release>%d</core_client_release>\n", major, minor, rel)
}

var (
	fileSizeRe = regexp.MustCompile(`<file_size>\s*([0-9.]+)\s*</file_size>`)
	statusRe   = regexp.MustCompile(`<status>\s*(-?\d+)\s*</status>`)
	messageRe  = regexp.MustCompile(`(?s)<message>(.*?)</message>`)
)

func uploadTo(ctx context.Context, target string, u ResultUpload, size int64, sum, clientVersion string, progress ProgressFunc) error {
	// 1. how much does the server already have?
	query := "<data_server_request>\n" + versionTags(clientVersion) +
		"    <get_file_size>" + u.Name + "</get_file_size>\n</data_server_request>\n"
	body, err := postText(ctx, target, strings.NewReader(query), int64(len(query)), nil)
	if err != nil {
		return err
	}
	offset := int64(0)
	if m := fileSizeRe.FindSubmatch(body); m != nil {
		if v, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
			offset = int64(v)
		}
	}
	if offset > size {
		offset = 0 // a longer file than ours is someone else's data: start over
	}
	if offset == size && size > 0 {
		if progress != nil {
			progress(size, size)
		}
		return nil // already complete on the server
	}

	// 2. the upload itself
	f, err := os.Open(u.Path)
	if err != nil {
		return &UploadError{Msg: fmt.Sprintf("open %s: %v", u.Path, err), Permanent: true}
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return &UploadError{Msg: fmt.Sprintf("seek %s: %v", u.Path, err), Permanent: true}
		}
	}
	sig := strings.TrimSpace(u.Signature)
	if sig != "" {
		sig += "\n"
	}
	head := "<data_server_request>\n" + versionTags(clientVersion) +
		"<file_upload>\n<file_info>\n" +
		"<name>" + u.Name + "</name>\n" +
		"<xml_signature>\n" + sig + "</xml_signature>\n" +
		fmt.Sprintf("<max_nbytes>%.0f</max_nbytes>\n", u.MaxNBytes) +
		"</file_info>\n" +
		fmt.Sprintf("<nbytes>%d</nbytes>\n", size) +
		"<md5_cksum>" + sum + "</md5_cksum>\n" +
		fmt.Sprintf("<offset>%d</offset>\n", offset) +
		"<data>\n"

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()
	stall := time.AfterFunc(stallTimeout, cancel)
	defer stall.Stop()
	ir := &idleReader{r: f, done: offset, total: size, progress: progress, timer: stall}
	reader := io.MultiReader(strings.NewReader(head), ir)
	reply, err := postText(ctx2, target, reader, int64(len(head))+size-offset, nil)
	if err != nil {
		return err
	}
	status := 0
	if m := statusRe.FindSubmatch(reply); m != nil {
		status, _ = strconv.Atoi(string(m[1]))
	} else {
		return &UploadError{Msg: "the upload server sent no status: " + snippet(reply)}
	}
	if status == 0 {
		return nil
	}
	msg := fmt.Sprintf("the upload server refused %s (status %d)", u.Name, status)
	if m := messageRe.FindSubmatch(reply); m != nil {
		msg += ": " + strings.TrimSpace(string(m[1]))
	}
	// The handler's own convention: -1 = permanent failure, 1 = try again.
	return &UploadError{Msg: msg, Permanent: status == -1}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(bytes.ToValidUTF8(b, []byte("?"))))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func postText(ctx context.Context, target string, body io.Reader, length int64, _ http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return nil, &UploadError{Msg: fmt.Sprintf("upload address %q: %v", target, err), Permanent: true}
	}
	req.Header.Set("Content-Type", "text/xml")
	req.ContentLength = length
	resp, err := transferClient.Do(req)
	if err != nil {
		return nil, &UploadError{Msg: "upload to " + target + ": " + err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, &UploadError{Msg: fmt.Sprintf("upload to %s: HTTP %d (%s)", target, resp.StatusCode, snippet(raw))}
	}
	return raw, nil
}
