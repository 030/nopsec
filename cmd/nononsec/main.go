package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	cdx "github.com/CycloneDX/cyclonedx-go"
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

	GenerateAllSBOMs()
}

type GoModule struct {
	Path     string
	Version  string
	Indirect bool
	Replace  *struct {
		Path    string
		Version string
	}
}

type GoPackage struct {
	Module *GoModule
}

// loadModuleIndirectMap loads the indirect flag for all modules in the repo (go list -m -json all)
func loadModuleIndirectMap(root string) (map[string]bool, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GO111MODULE=on")

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m -json all failed: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(out))
	indirectMap := make(map[string]bool)
	for dec.More() {
		var mod GoModule
		if err := dec.Decode(&mod); err != nil {
			return nil, err
		}
		path := mod.Path
		version := mod.Version
		if mod.Replace != nil {
			path = mod.Replace.Path
			version = mod.Replace.Version
		}
		key := path + "@" + version
		indirectMap[key] = mod.Indirect
	}

	return indirectMap, nil
}

// findApps looks for dirs under cmd/ with main.go file
func findApps(cmdDir string) ([]string, error) {
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return nil, err
	}
	var apps []string
	for _, e := range entries {
		if e.IsDir() {
			mainGo := filepath.Join(cmdDir, e.Name(), "main.go")
			if fi, err := os.Stat(mainGo); err == nil && !fi.IsDir() {
				apps = append(apps, filepath.Join(cmdDir, e.Name()))
			}
		}
	}
	return apps, nil
}

// listUsedModules runs `go list -json -deps ./...` in dir and collects all used modules
func listUsedModules(dir string) (map[string]GoModule, error) {
	cmd := exec.Command("go", "list", "-json", "-deps", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO111MODULE=on")

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list failed in %s: %w", dir, err)
	}

	dec := json.NewDecoder(bytes.NewReader(out))
	modules := make(map[string]GoModule)

	for dec.More() {
		var pkg GoPackage
		if err := dec.Decode(&pkg); err != nil {
			return nil, err
		}
		if pkg.Module == nil {
			continue
		}
		mod := *pkg.Module
		// Use replaced module path/version if any
		if mod.Replace != nil {
			mod.Path = mod.Replace.Path
			mod.Version = mod.Replace.Version
		}
		if mod.Version == "" {
			mod.Version = "unknown"
		}
		key := mod.Path + "@" + mod.Version
		if _, exists := modules[key]; !exists {
			modules[key] = mod
		}
	}

	return modules, nil
}

func generateSBOM(appDir string, modules map[string]GoModule, indirectMap map[string]bool) error {
	appName := filepath.Base(appDir)

	var comps []cdx.Component
	for _, mod := range modules {
		key := mod.Path + "@" + mod.Version
		indirect := indirectMap[key]

		componentsType := cdx.ComponentTypeLibrary
		name := mod.Path
		version := mod.Version

		c := cdx.Component{
			Type:       componentsType,
			Name:       name,
			Version:    version,
			PackageURL: fmt.Sprintf("pkg:golang/%s@%s", name, version),
		}

		if indirect {
			// Add indirect info as evidence in the properties
			c.Properties = &[]cdx.Property{
				{
					Name:  "indirect",
					Value: "true",
				},
			}
		}

		comps = append(comps, c)
	}

	bom := &cdx.BOM{
		SpecVersion: cdx.SpecVersion1_6,
		Version:     1,
		Components:  &comps,
		Metadata: &cdx.Metadata{
			Tools: &cdx.ToolsChoice{
				Components: &[]cdx.Component{{
					Type:    cdx.ComponentTypeApplication,
					Name:    appName,
					Version: "1.0.0",
					Supplier: &cdx.OrganizationalEntity{
						Name: "SBOM Generator",
					},
				}},
			},
		},
	}

	outFile := fmt.Sprintf("sbom-%s.json", appName)
	f, err := os.Create(outFile)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := cdx.NewBOMEncoder(f, cdx.BOMFileFormatJSON)
	enc.SetPretty(true)
	return enc.Encode(bom)
}

func GenerateAllSBOMs() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get working dir: %v\n", err)
		os.Exit(1)
	}

	cmdDir := filepath.Join(root, "cmd")
	apps, err := findApps(cmdDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to find apps in %s: %v\n", cmdDir, err)
		os.Exit(1)
	}

	if len(apps) == 0 {
		fmt.Fprintf(os.Stderr, "no apps found in %s\n", cmdDir)
		os.Exit(1)
	}

	indirectMap, err := loadModuleIndirectMap(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load indirect modules map: %v\n", err)
		os.Exit(1)
	}

	for _, app := range apps {
		fmt.Printf("Generating SBOM for app: %s\n", filepath.Base(app))
		modules, err := listUsedModules(app)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list used modules for %s: %v\n", app, err)
			continue
		}
		err = generateSBOM(app, modules, indirectMap)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to generate SBOM for %s: %v\n", app, err)
		} else {
			fmt.Printf("SBOM for %s written\n", filepath.Base(app))
		}
	}
}
