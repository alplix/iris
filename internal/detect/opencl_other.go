//go:build !(windows && (amd64 || arm64))

package detect

// openCLDevices is only implemented for Windows (which loads OpenCL.dll
// directly). Elsewhere Iris has no way to call the OpenCL driver without cgo,
// so no OpenCL device is claimed.
func openCLDevices() []OpenCLDevice { return nil }
