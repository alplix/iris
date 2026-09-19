package xml

import (
	"fmt"
	"regexp"
)

var replyRe = regexp.MustCompile(`(?s)^<boinc_gui_rpc_reply>(.*)</boinc_gui_rpc_reply>\s*$`)

func WrapReply(inner string) string {
	return fmt.Sprintf("<boinc_gui_rpc_reply>\n%s\n</boinc_gui_rpc_reply>", inner)
}

func WrapSuccess() string {
	return WrapReply("<success/>")
}

func WrapError(msg string) string {
	return WrapReply(fmt.Sprintf("<error>%s</error>", Escape(msg)))
}

func Escape(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			out = append(out, "&amp;"...)
		case '<':
			out = append(out, "&lt;"...)
		case '>':
			out = append(out, "&gt;"...)
		case '"':
			out = append(out, "&quot;"...)
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

func StripReply(frame []byte) []byte {
	s := replyRe.FindStringSubmatch(string(frame))
	if s != nil {
		return []byte(s[1])
	}
	return frame
}
