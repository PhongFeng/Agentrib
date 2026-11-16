package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Project struct {
	Repository string `json:"repository"`
	GodotDir   string `json:"godot_dir,omitempty"`
	TestScript string `json:"test_script,omitempty"`
	WebPreset  string `json:"web_preset,omitempty"`
}

type Config struct {
	Server    string             `json:"server"`
	State     string             `json:"state"`
	TokenFile string             `json:"token_file"`
	Commands  map[string]string  `json:"commands"`
	Projects  map[string]Project `json:"projects"`
	token     string
}

var projectKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
var runnerToken = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Load(path string) (Config, error) {
	var c Config
	file, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	base, err := url.Parse(c.Server)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Path != "" && base.Path != "/") || (base.Scheme != "https" && !(base.Scheme == "http" && base.Hostname() == "127.0.0.1")) {
		return c, errors.New("server must be an HTTPS origin (HTTP is supported on loopback for development)")
	}
	c.Server = strings.TrimRight(c.Server, "/")
	if !filepath.IsAbs(c.State) || len(c.Projects) == 0 || len(c.Projects) > 64 {
		return c, errors.New("provide an absolute state directory and 1–64 projects")
	}
	for key, project := range c.Projects {
		if !projectKey.MatchString(key) || !filepath.IsAbs(project.Repository) || !safeRelative(project.GodotDir) ||
			!safeRelative(project.TestScript) || !strings.HasSuffix(project.TestScript, ".gd") {
			return c, fmt.Errorf("invalid configuration for project %q (repository and Godot test script are required)", key)
		}
	}
	c.token = strings.TrimSpace(os.Getenv("AGENTRIB_RUNNER_TOKEN"))
	if c.token == "" {
		if !filepath.IsAbs(c.TokenFile) {
			return c, errors.New("provide an absolute token_file or set AGENTRIB_RUNNER_TOKEN")
		}
		token, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return c, err
		}
		c.token = strings.TrimSpace(string(token))
	}
	if !runnerToken.MatchString(c.token) {
		return c, errors.New("invalid runner token")
	}
	return c, nil
}

func safeRelative(path string) bool {
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || strings.ContainsRune(path, 0) {
		return false
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return true
}

func contained(root, path string) (string, error) {
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, actual)
	if err != nil || !safeRelative(rel) {
		return "", errors.New("path leaves the project workspace")
	}
	return actual, nil
}
