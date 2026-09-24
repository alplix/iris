package detect

import "testing"

func TestParseNvidiaDetails(t *testing.T) {
	d := parseNvidiaDetails("12.0, 616.92\r\n", "| NVIDIA-SMI 616.92   Driver Version: 616.92   CUDA Version: 13.0 |")
	if d.CCMajor != 12 || d.CCMinor != 0 || d.Driver != "616.92" || d.CudaVersion != 13000 {
		t.Errorf("got %+v", d)
	}
	if d := parseNvidiaDetails("8.9, 550.1", "KMD Version: 616.92        CUDA UMD Version: 13.4  |"); d.CudaVersion != 13040 {
		t.Errorf("the newer banner wording must parse, got %+v", d)
	}
	if z := parseNvidiaDetails("", ""); z != (NvidiaDetails{}) {
		t.Errorf("nothing detected must claim nothing, got %+v", z)
	}
}
