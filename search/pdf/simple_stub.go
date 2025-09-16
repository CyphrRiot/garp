////go:build !pdfcpu
//go:build !pdfcpu
// +build !pdfcpu

package pdf

// ExtractAllTextCapped is a stub used for default builds without the "pdfcpu" tag.
// It exists to keep the codebase compiling while PDF functionality is disabled.
// For PDF-enabled builds, see the implementation in simple.go (guarded by "pdfcpu" build tag).
func ExtractAllTextCapped(path string, pageCap, perPageCap int) (string, error) {
	return "", ErrPDFDisabled
}
