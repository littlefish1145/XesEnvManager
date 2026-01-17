package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"python-manager/pkg/model"
	"python-manager/pkg/utils"
	"python-manager/pkg/wsl"
)

// Pre-compiled regex patterns for performance
var (
	versionRegexOnce sync.Once
	versionRegex     *regexp.Regexp
	versionRegex2    *regexp.Regexp
)

// getVersionRegex returns the compiled version regex patterns
func getVersionRegex() (*regexp.Regexp, *regexp.Regexp) {
	versionRegexOnce.Do(func() {
		versionRegex = regexp.MustCompile(`Python (\d+\.\d+\.\d+)`)
		versionRegex2 = regexp.MustCompile(`Python (\d+\.\d+\.\d+\S*)`)
	})
	return versionRegex, versionRegex2
}

// PythonConfig manages Python environment configurations
type PythonConfig struct {
	configFilePath         string
	pythonEnvironments     []model.PythonEnvironment
	currentEnvironmentName string
	DisableUpdateCheck     bool
	AutoUpdate             bool
}

// NewPythonConfig creates a new PythonConfig instance
func NewPythonConfig(configPath string) *PythonConfig {
	config := &PythonConfig{
		currentEnvironmentName: "system",
		DisableUpdateCheck:     false,
		AutoUpdate:             false,
	}

	if configPath == "" {
		appDataPath := utils.GetAppDataPath()
		if appDataPath != "" {
			configDir := filepath.Join(appDataPath, "PythonManager")
			_ = os.MkdirAll(configDir, 0755)
			config.configFilePath = filepath.Join(configDir, "python_config.json")
		} else {
			config.configFilePath = "python_config.json"
		}
	} else {
		config.configFilePath = configPath
	}

	return config
}

// LoadOrCreateConfig loads config file, creates if it doesn't exist
func (c *PythonConfig) LoadOrCreateConfig(autoSearch bool) bool {
	if _, err := os.Stat(c.configFilePath); os.IsNotExist(err) {
		fmt.Printf("配置文件不存在: %s\n", c.configFilePath)
		if autoSearch {
			fmt.Println("正在搜索Python环境...")
			// Config file doesn't exist, create default config
			c.pythonEnvironments = nil

			// Search Python environments
			c.SearchPythonEnvironments()

			// Save config
			return c.saveConfigFile(c.configFilePath)
		} else {
			fmt.Println("请使用 '-l' 命令搜索Python环境并生成配置文件。")
			return false
		}
	} else {
		fmt.Printf("正在加载配置文件: %s\n", c.configFilePath)
		parseResult := c.parseConfigFile(c.configFilePath)

		// Check if config file is valid (contains at least one Python environment)
		if parseResult && len(c.pythonEnvironments) == 0 {
			fmt.Println("配置文件无效：未找到任何Python环境。")
			fmt.Println("请使用 '-l' 命令重新搜索并生成配置文件。")
			return false
		}

		return parseResult
	}
}

// parseConfigFile parses the JSON config file
func (c *PythonConfig) parseConfigFile(filePath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	data, err := ioutil.ReadAll(file)
	if err != nil {
		return false
	}

	var configData struct {
		CurrentEnvironment string                    `json:"current_environment"`
		Environments       []model.PythonEnvironment `json:"environments"`
		DisableUpdateCheck bool                      `json:"disable_update_check"`
		AutoUpdate         bool                      `json:"auto_update"`
	}

	if err := json.Unmarshal(data, &configData); err != nil {
		return false
	}

	c.currentEnvironmentName = configData.CurrentEnvironment
	if c.currentEnvironmentName == "" {
		c.currentEnvironmentName = "system"
	}

	c.pythonEnvironments = configData.Environments
	c.DisableUpdateCheck = configData.DisableUpdateCheck
	c.AutoUpdate = configData.AutoUpdate

	return true
}

// saveConfigFile saves the config to JSON file
func (c *PythonConfig) saveConfigFile(filePath string) bool {
	configData := struct {
		CurrentEnvironment string                    `json:"current_environment"`
		Environments       []model.PythonEnvironment `json:"environments"`
		DisableUpdateCheck bool                      `json:"disable_update_check"`
		AutoUpdate         bool                      `json:"auto_update"`
	}{
		CurrentEnvironment: c.currentEnvironmentName,
		Environments:       c.pythonEnvironments,
		DisableUpdateCheck: c.DisableUpdateCheck,
		AutoUpdate:         c.AutoUpdate,
	}

	data, err := json.MarshalIndent(configData, "", "  ")
	if err != nil {
		return false
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return false
	}
	defer file.Close()

	_, err = file.Write(data)
	return err == nil
}

// GetPythonVersion gets the Python version from the executable
func (c *PythonConfig) GetPythonVersion(pythonPath string) string {
	cmd := exec.Command(pythonPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}

	versionOutput := string(output)
	// Parse version, e.g. from "Python 3.9.7" extract "3.9.7"
	versionRegex, versionRegex2 := getVersionRegex()
	matches := versionRegex.FindStringSubmatch(versionOutput)
	if len(matches) > 1 {
		return matches[1]
	}

	// Try alternative format "Python 3.9.7+" (with possible revision)
	matches = versionRegex2.FindStringSubmatch(versionOutput)
	if len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// findSystemPython finds system Python installations
func (c *PythonConfig) findSystemPython() string {
	// Try common system Python paths
	commonPaths := []string{
		"python.exe",
		"python3.exe",
		"C:\\Python312\\python.exe",
		"C:\\Python311\\python.exe",
		"C:\\Python310\\python.exe",
		"C:\\Python39\\python.exe",
		"C:\\Python38\\python.exe",
		"C:\\Program Files\\Python312\\python.exe",
		"C:\\Program Files\\Python311\\python.exe",
		"C:\\Program Files\\Python310\\python.exe",
		"C:\\Program Files\\Python39\\python.exe",
		"C:\\Program Files\\Python38\\python.exe",
		"C:\\Program Files (x86)\\Python312\\python.exe",
		"C:\\Program Files (x86)\\Python311\\python.exe",
		"C:\\Program Files (x86)\\Python310\\python.exe",
		"C:\\Program Files (x86)\\Python39\\python.exe",
		"C:\\Program Files (x86)\\Python38\\python.exe",
	}

	// Check PATH environment variable
	pathEnv := os.Getenv("PATH")
	if pathEnv != "" {
		paths := strings.Split(pathEnv, ";")
		for _, path := range paths {
			// Check if python.exe exists in this path
			pythonPath := filepath.Join(path, "python.exe")
			if fileExists(pythonPath) {
				commonPaths = append([]string{pythonPath}, commonPaths...)
			}

			// Check for python3.exe
			python3Path := filepath.Join(path, "python3.exe")
			if fileExists(python3Path) {
				commonPaths = append([]string{python3Path}, commonPaths...)
			}
		}
	}

	// Check each path
	for _, path := range commonPaths {
		if fileExists(path) {
			absPath, err := filepath.Abs(path)
			if err == nil {
				return absPath
			}
			return path
		}
	}

	return ""
}

// findCondaEnvironments finds conda environments
func (c *PythonConfig) findCondaEnvironments() []string {
	var condaEnvs []string

	condaExe := os.Getenv("CONDA_EXE")
	if condaExe != "" {
		condaEnvs = append(condaEnvs, getCondaEnvListFromExe(condaExe)...)
	}

	if len(condaEnvs) == 0 {
		userProfile := os.Getenv("USERPROFILE")
		if userProfile != "" {
			condaEnvs = append(condaEnvs, searchMinicondaEnvsWindows(userProfile)...)
			condaEnvs = append(condaEnvs, searchAnacondaEnvsWindows(userProfile)...)
		}
	}

	return condaEnvs
}

func getCondaEnvListFromExe(condaExe string) []string {
	var condaEnvs []string

	cmd := exec.Command(condaExe, "env", "list")
	output, err := cmd.Output()
	if err != nil {
		return condaEnvs
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if line == "*" || strings.HasPrefix(line, "*") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 2 {
			envName := parts[0]
			envPath := parts[1]

			if envName == "*" {
				continue
			}

			pythonPath := filepath.Join(envPath, "python.exe")
			if fileExists(pythonPath) {
				condaEnvs = append(condaEnvs, pythonPath)
			}
		}
	}

	return condaEnvs
}

func searchMinicondaEnvsWindows(userProfile string) []string {
	var condaEnvs []string

	minicondaPath := filepath.Join(userProfile, "Miniconda3")
	if fileExists(minicondaPath) {
		pythonPath := filepath.Join(minicondaPath, "python.exe")
		if fileExists(pythonPath) {
			condaEnvs = append(condaEnvs, pythonPath)
		}

		envsDir := filepath.Join(minicondaPath, "envs")
		if fileExists(envsDir) {
			file, err := os.Open(envsDir)
			if err == nil {
				defer file.Close()
				entries, err := file.Readdir(-1)
				if err == nil {
					for _, entry := range entries {
						if entry.IsDir() {
							pythonPath := filepath.Join(envsDir, entry.Name(), "python.exe")
							if fileExists(pythonPath) {
								condaEnvs = append(condaEnvs, pythonPath)
							}
						}
					}
				}
			}
		}
	}

	return condaEnvs
}

func searchAnacondaEnvsWindows(userProfile string) []string {
	var condaEnvs []string

	anacondaPath := filepath.Join(userProfile, "Anaconda3")
	if fileExists(anacondaPath) {
		pythonPath := filepath.Join(anacondaPath, "python.exe")
		if fileExists(pythonPath) {
			condaEnvs = append(condaEnvs, pythonPath)
		}

		envsDir := filepath.Join(anacondaPath, "envs")
		if fileExists(envsDir) {
			file, err := os.Open(envsDir)
			if err == nil {
				defer file.Close()
				entries, err := file.Readdir(-1)
				if err == nil {
					for _, entry := range entries {
						if entry.IsDir() {
							pythonPath := filepath.Join(envsDir, entry.Name(), "python.exe")
							if fileExists(pythonPath) {
								condaEnvs = append(condaEnvs, pythonPath)
							}
						}
					}
				}
			}
		}
	}

	return condaEnvs
}

// findUVEnvironments finds uv environments
func (c *PythonConfig) findUVEnvironments() []string {
	var uvEnvs []string

	// Look for uv command in PATH
	pathEnv := os.Getenv("PATH")
	if pathEnv != "" {
		paths := strings.Split(pathEnv, ";")
		for _, path := range paths {
			uvPath := filepath.Join(path, "uv.exe")
			if fileExists(uvPath) {
				// Found uv, now find its managed Python environments
				localAppData := os.Getenv("LOCALAPPDATA")
				if localAppData != "" {
					uvEnvPath := filepath.Join(localAppData, "uv", "python")
					if fileExists(uvEnvPath) {
						// Walk through the directory recursively
						filepath.Walk(uvEnvPath, func(path string, info os.FileInfo, err error) error {
							if err != nil {
								return nil
							}
							if !info.IsDir() && info.Name() == "python.exe" {
								uvEnvs = append(uvEnvs, path)
							}
							return nil
						})
					}
				}
				break
			}
		}
	}

	return uvEnvs
}

// findVenvEnvironments finds venv environments
func (c *PythonConfig) findVenvEnvironments() []string {
	var venvEnvs []string

	currentPath, _ := os.Getwd()
	currentPath = filepath.Clean(currentPath)

	venvEnvs = append(venvEnvs, searchVenvInPathTreeWindows(currentPath)...)

	userProfile := os.Getenv("USERPROFILE")
	if userProfile != "" {
		venvEnvs = append(venvEnvs, searchVenvInHomeWindows(userProfile)...)
	}

	venvEnvs = append(venvEnvs, searchVenvInPATH()...)
	venvEnvs = append(venvEnvs, searchVenvInVirtualEnv()...)

	return venvEnvs
}

func searchVenvInPathTreeWindows(currentPath string) []string {
	var venvEnvs []string

	for i := 0; i < 5; i++ {
		venvNames := []string{"venv", ".venv", "env", ".env"}

		for _, venvName := range venvNames {
			venvPath := filepath.Join(currentPath, venvName)
			if fileExists(venvPath) {
				pythonPath := filepath.Join(venvPath, "Scripts", "python.exe")
				if fileExists(pythonPath) {
					venvEnvs = append(venvEnvs, pythonPath)
				}
			}
		}

		parentPath := filepath.Dir(currentPath)
		if parentPath == currentPath {
			break
		}
		currentPath = parentPath
	}

	return venvEnvs
}

func searchVenvInHomeWindows(userProfile string) []string {
	var venvEnvs []string

	venvNames := []string{"venv", ".venv", "env", ".env"}
	for _, venvName := range venvNames {
		venvPath := filepath.Join(userProfile, venvName)
		if fileExists(venvPath) {
			pythonPath := filepath.Join(venvPath, "Scripts", "python.exe")
			if fileExists(pythonPath) {
				venvEnvs = append(venvEnvs, pythonPath)
			}
		}
	}

	return venvEnvs
}

func searchVenvInPATH() []string {
	var venvEnvs []string

	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return venvEnvs
	}

	paths := strings.Split(pathEnv, ";")
	for _, path := range paths {
		if strings.Contains(path, "\\Scripts") {
			pythonPath := filepath.Join(path, "python.exe")
			if fileExists(pythonPath) {
				venvEnvs = append(venvEnvs, pythonPath)
			}
		}
	}

	return venvEnvs
}

func searchVenvInVirtualEnv() []string {
	var venvEnvs []string

	virtualEnv := os.Getenv("VIRTUAL_ENV")
	if virtualEnv != "" {
		pythonPath := filepath.Join(virtualEnv, "Scripts", "python.exe")
		if fileExists(pythonPath) {
			venvEnvs = append(venvEnvs, pythonPath)
		}
	}

	return venvEnvs
}

// SearchPythonEnvironments searches all available Python environments
func (c *PythonConfig) SearchPythonEnvironments() {
	versionCache := c.buildVersionCache()

	c.pythonEnvironments = nil
	processedPaths := make(map[string]bool)

	addEnvironmentIfUnique := c.createEnvironmentAdder(versionCache, processedPaths)

	addSystemPython(addEnvironmentIfUnique)
	addCondaEnvironments(addEnvironmentIfUnique)
	addUVEnvironments(addEnvironmentIfUnique)
	addVenvEnvironments(addEnvironmentIfUnique)
	addWSLEnvironments(addEnvironmentIfUnique, processedPaths)

	c.saveConfigFile(c.configFilePath)
}

func (c *PythonConfig) buildVersionCache() map[string]string {
	versionCache := make(map[string]string)
	for _, env := range c.pythonEnvironments {
		if env.Version != "" {
			versionCache[env.Path] = env.Version
		}
	}
	return versionCache
}

func (c *PythonConfig) createEnvironmentAdder(versionCache map[string]string, processedPaths map[string]bool) func(name, path string, enabled bool, platform string) {
	return func(name, path string, enabled bool, platform string) {
		if path != "" && !processedPaths[path] {
			version := versionCache[path]
			if version == "" {
				version = c.GetPythonVersion(path)
			}

			env := model.PythonEnvironment{
				Name:     name,
				Path:     path,
				Version:  version,
				Enabled:  enabled,
				Encoding: "utf-8",
				Platform: platform,
			}

			c.pythonEnvironments = append(c.pythonEnvironments, env)
			processedPaths[path] = true
		}
	}
}

func addSystemPython(addEnvironment func(name, path string, enabled bool, platform string)) {
	systemPython := func() string {
		cfg := &PythonConfig{}
		return cfg.findSystemPython()
	}()

	if systemPython != "" {
		addEnvironment("system", systemPython, true, "windows")
	}
}

func addCondaEnvironments(addEnvironment func(name, path string, enabled bool, platform string)) {
	cfg := &PythonConfig{}
	condaPythons := cfg.findCondaEnvironments()
	for i, condaPython := range condaPythons {
		addEnvironment(fmt.Sprintf("conda_%d", i), condaPython, false, "windows")
	}
}

func addUVEnvironments(addEnvironment func(name, path string, enabled bool, platform string)) {
	cfg := &PythonConfig{}
	uvPythons := cfg.findUVEnvironments()
	for i, uvPython := range uvPythons {
		addEnvironment(fmt.Sprintf("uv_%d", i), uvPython, false, "windows")
	}
}

func addVenvEnvironments(addEnvironment func(name, path string, enabled bool, platform string)) {
	cfg := &PythonConfig{}
	venvPythons := cfg.findVenvEnvironments()
	for i, venvPython := range venvPythons {
		addEnvironment(fmt.Sprintf("venv_%d", i), venvPython, false, "windows")
	}
}

func addWSLEnvironments(addEnvironment func(name, path string, enabled bool, platform string), processedPaths map[string]bool) {
	cfg := &PythonConfig{}
	wslEnvironments, err := cfg.SearchWSLEnvironments()
	if err == nil {
		for _, env := range wslEnvironments {
			if !processedPaths[env.Path] {
				env.Platform = "wsl"
				cfg.pythonEnvironments = append(cfg.pythonEnvironments, env)
				processedPaths[env.Path] = true
			}
		}
	}
}

// IsWSLAvailable checks if WSL is available and the server is running
func IsWSLAvailable() bool {
	// Check if WSL is available by trying to connect to the default server port
	return wsl.TestWSLConnection("localhost:50051")
}

// SearchWSLEnvironments searches for Python environments via WSL server
func (c *PythonConfig) SearchWSLEnvironments() ([]model.PythonEnvironment, error) {
	// First check if WSL server is available silently
	if !IsWSLAvailable() {
		return nil, nil // Silently skip if not available
	}

	client, err := wsl.NewWSLClient("localhost:50051")
	if err != nil {
		return nil, err
	}
	defer client.Close()

	environments, err := client.SearchEnvironments()
	if err != nil {
		return nil, err
	}

	// Add "wsl_" prefix to all WSL environment names to identify them
	for i := range environments {
		environments[i].Name = "wsl_" + environments[i].Name
		environments[i].Platform = "wsl"
	}

	return environments, nil
}

// SetDisableUpdateCheck sets the update check preference
func (c *PythonConfig) SetDisableUpdateCheck(disabled bool) bool {
	c.DisableUpdateCheck = disabled
	return c.saveConfigFile(c.configFilePath)
}

// GetDisableUpdateCheck returns the update check preference
func (c *PythonConfig) GetDisableUpdateCheck() bool {
	return c.DisableUpdateCheck
}

// SetAutoUpdate sets the auto update preference
func (c *PythonConfig) SetAutoUpdate(auto bool) bool {
	c.AutoUpdate = auto
	return c.saveConfigFile(c.configFilePath)
}

// GetAutoUpdate returns the auto update preference
func (c *PythonConfig) GetAutoUpdate() bool {
	return c.AutoUpdate
}

// GetPythonEnvironments returns all Python environments
func (c *PythonConfig) GetPythonEnvironments() []model.PythonEnvironment {
	return c.pythonEnvironments
}

// SetCurrentEnvironment sets the current environment
func (c *PythonConfig) SetCurrentEnvironment(envName string) bool {
	for _, env := range c.pythonEnvironments {
		if env.Name == envName && env.Enabled {
			c.currentEnvironmentName = envName
			return c.saveConfigFile(c.configFilePath)
		}
	}
	return false
}

// GetCurrentPythonPath returns the Python path for the current environment
func (c *PythonConfig) GetCurrentPythonPath() string {
	for _, env := range c.pythonEnvironments {
		if env.Name == c.currentEnvironmentName && env.Enabled {
			return env.Path
		}
	}

	// If current environment is not available, try to return first available environment
	for _, env := range c.pythonEnvironments {
		if env.Enabled {
			return env.Path
		}
	}

	return ""
}

// GetCurrentEnvironment returns the current environment info
func (c *PythonConfig) GetCurrentEnvironment() model.PythonEnvironment {
	for _, env := range c.pythonEnvironments {
		if env.Name == c.currentEnvironmentName && env.Enabled {
			return env
		}
	}

	// If current environment is not available, return first available environment
	for _, env := range c.pythonEnvironments {
		if env.Enabled {
			return env
		}
	}

	// Return empty environment
	return model.PythonEnvironment{}
}

// DisplayCurrentEnvironment displays current Python environment info
func (c *PythonConfig) DisplayCurrentEnvironment() {
	currentEnv := c.GetCurrentEnvironment()

	if currentEnv.Name == "" {
		fmt.Println("未找到可用的Python环境")
		return
	}

	fmt.Println("当前Python环境信息:")
	fmt.Printf("  名称: %s\n", currentEnv.Name)
	fmt.Printf("  路径: %s\n", currentEnv.Path)
	fmt.Printf("  版本: %s\n", func() string {
		if currentEnv.Version == "" {
			return "未知"
		}
		return currentEnv.Version
	}())
	fmt.Printf("  编码: %s\n", currentEnv.Encoding)
	fmt.Printf("  状态: %s\n", func() string {
		if currentEnv.Enabled {
			return "已启用"
		}
		return "已禁用"
	}())
}

// ToggleEnvironment enables/disables an environment
func (c *PythonConfig) ToggleEnvironment(envName string, enabled bool) bool {
	for i := range c.pythonEnvironments {
		if c.pythonEnvironments[i].Name == envName {
			c.pythonEnvironments[i].Enabled = enabled
			return c.saveConfigFile(c.configFilePath)
		}
	}
	return false
}

// UpdateEnvironment updates an environment
func (c *PythonConfig) UpdateEnvironment(envName string, newEnv model.PythonEnvironment) bool {
	for i := range c.pythonEnvironments {
		if c.pythonEnvironments[i].Name == envName {
			c.pythonEnvironments[i] = newEnv
			return c.saveConfigFile(c.configFilePath)
		}
	}
	return false
}

// GetConfigPath returns the config file path
func (c *PythonConfig) GetConfigPath() string {
	return c.configFilePath
}

// Helper function to check if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}
