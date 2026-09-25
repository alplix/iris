//go:build !(windows && (amd64 || arm64))

package detect

// openCLDevices asks the clinfo tool (Iris cannot load the OpenCL library itself
// without cgo); no clinfo installed means no OpenCL device is claimed.
func openCLDevices() []OpenCLDevice { return clinfoDevices() }
