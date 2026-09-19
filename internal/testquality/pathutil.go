package testquality

import "path/filepath"

func filepathAbs(path string) (string, error) {
	return filepath.Abs(path)
}

func filepathClean(path string) string {
	return filepath.Clean(path)
}

func filepathRel(base, target string) (string, error) {
	return filepath.Rel(base, target)
}
