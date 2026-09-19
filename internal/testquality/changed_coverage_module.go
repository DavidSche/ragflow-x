package testquality

import (
	"os"
	"path/filepath"
	"strings"
)

func modulePathFromRepository(repositoryDir string) string {
	data, err := os.ReadFile(filepath.Join(repositoryDir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, rawLine := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(rawLine))
		if len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "`\"")
		}
	}
	return ""
}
