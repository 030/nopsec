package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

func isDockerfile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineCount := 0

	for ; scanner.Scan() && lineCount < 20; lineCount++ {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(strings.ToUpper(line), "FROM ") {
			return true
		}
	}

	return false
}

func checkFileType(filename, fullPath string, found map[string]bool) {
	switch filename {
	case "go.mod":
		found["go"] = true
	case "package.json":
		found["nodejs"] = true
	case "requirements.txt", "setup.py":
		found["python"] = true
	case "pom.xml", "build.gradle":
		found["java"] = true
	default:
		if strings.HasSuffix(filename, ".go") {
			found["go"] = true
		}

		if strings.HasPrefix(filename, "Dockerfile") && isDockerfile(fullPath) {
			found["docker"] = true
		}
	}
}

func detectProjectType(root string) (string, error) {
	found := make(map[string]bool)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walking path %s: %w", path, walkErr)
		}

		if entry.IsDir() {
			return nil
		}

		filename := filepath.Base(path)

		checkFileType(filename, path, found)

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to walk project directory: %w", err)
	}

	if len(found) == 0 {
		return "unknown", nil
	}

	types := make([]string, 0, len(found))

	for projectType := range found {
		types = append(types, projectType)
	}

	return strings.Join(types, "+"), nil
}

func main() {
	projectRoot := "./"

	projectType, err := detectProjectType(projectRoot)
	if err != nil {
		log.Errorf("Project detection failed: %v", err)

		return
	}

	log.Infof("Detected project type: %s", projectType)
}
