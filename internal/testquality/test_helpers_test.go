package testquality

import (
	"encoding/json"
	"os"
)

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
