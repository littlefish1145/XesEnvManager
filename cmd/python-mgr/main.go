//go:build !linux && !unix
// +build !linux,!unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"python-manager/pkg/config"
	"python-manager/pkg/executor"
)

// Global variables
var (
	cfg *config.PythonConfig
)

func main() {
	// Set console encoding to UTF-8 (for Windows)
	// On Windows, we could set code page to 65001 (UTF-8) but Go handles UTF-8 natively

	// Initialize config
	cfg = config.NewPythonConfig("")

	// Parse command line arguments to see if we need to create config
	needCreateConfig := false
	hasPythonScript := false

	// Check all arguments
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--list" || arg == "-l" {
			needCreateConfig = true
		}
		// Check if this looks like a Python script (has .py extension)
		if strings.HasSuffix(arg, ".py") {
			hasPythonScript = true
		}
	}

	// If there are Python script arguments, then don't automatically search
	// This avoids unwanted searching when other apps pass -l in Python args
	if hasPythonScript {
		needCreateConfig = false
	}

	// Load or create config file
	if !cfg.LoadOrCreateConfig(needCreateConfig) {
		fmt.Fprintln(os.Stderr, "无法加载或创建配置文件")
		os.Exit(1)
	}

	// Now re-detect Python scripts for argument processing
	hasPythonScriptForArgs := false
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasSuffix(arg, ".py") {
			hasPythonScriptForArgs = true
			break
		}
	}

	// Parse command line arguments
	var pythonArgs []string
	captureOutput := true
	autoExit := true
	showInfo := false

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]

		if arg == "--help" || arg == "-h" {
			showHelp()
			return
		} else if arg == "--list" || arg == "-l" {
			// If there are Python script arguments, treat -l as Python script argument, not list command
			if hasPythonScriptForArgs {
				pythonArgs = append(pythonArgs, arg)
			} else {
				// Force re-search Python environments
				cfg.SearchPythonEnvironments()
				fmt.Println("搜索完成，已更新配置文件")
				listEnvironments(cfg)
				return
			}
		} else if arg == "--set" && i+1 < len(os.Args) {
			envName := os.Args[i+1]
			i++ // Skip the next argument
			if cfg.SetCurrentEnvironment(envName) {
				fmt.Printf("已设置当前Python环境为: %s\n", envName)
			} else {
				fmt.Fprintf(os.Stderr, "设置Python环境失败: %s\n", envName)
				os.Exit(1)
			}
			return
		} else if arg == "--enable" && i+1 < len(os.Args) {
			envName := os.Args[i+1]
			i++ // Skip the next argument
			if cfg.ToggleEnvironment(envName, true) {
				fmt.Printf("已启用Python环境: %s\n", envName)
			} else {
				fmt.Fprintf(os.Stderr, "启用Python环境失败: %s\n", envName)
				os.Exit(1)
			}
			return
		} else if arg == "--disable" && i+1 < len(os.Args) {
			envName := os.Args[i+1]
			i++ // Skip the next argument
			if cfg.ToggleEnvironment(envName, false) {
				fmt.Printf("已禁用Python环境: %s\n", envName)
			} else {
				fmt.Fprintf(os.Stderr, "禁用Python环境失败: %s\n", envName)
				os.Exit(1)
			}
			return
		} else if arg == "--search" || arg == "-s" {
			// If there are Python script arguments, treat -s as Python script argument, not search command
			if hasPythonScriptForArgs {
				pythonArgs = append(pythonArgs, arg)
			} else {
				fmt.Println("正在搜索Python环境...")
				cfg.SearchPythonEnvironments()
				fmt.Println("搜索完成，已更新配置文件")
				listEnvironments(cfg)
				return
			}
		} else if arg == "--info" || arg == "-i" {
			showInfo = true
		} else if arg == "--no-capture" {
			captureOutput = false
		} else if arg == "--no-exit" {
			autoExit = false
		} else if arg == "--edit" || arg == "-e" {
			// Open config file in notepad
			configPath := cfg.GetConfigPath()
			// Use proper way to open file with notepad to avoid issues with paths containing spaces
			cmd := exec.Command("notepad.exe", configPath)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err := cmd.Run()
			if err == nil {
				fmt.Printf("已在记事本中打开配置文件: %s\n", configPath)
			} else {
				fmt.Fprintf(os.Stderr, "打开配置文件失败: %v\n", err)
			}
			return
		} else {
			// Other arguments passed to Python
			pythonArgs = append(pythonArgs, arg)
		}
	}

	// If no Python arguments and no show info, display help
	if len(pythonArgs) == 0 && !showInfo {
		showHelp()
		return
	}

	// Display current environment info
	if showInfo {
		cfg.DisplayCurrentEnvironment()
	}

	// If no Python arguments, just display info
	if len(pythonArgs) == 0 {
		return
	}

	// Create executor and execute Python script
	exec := executor.NewPythonExecutor(cfg)
	exec.SetArguments(pythonArgs)
	exec.SetCaptureOutput(captureOutput)
	exec.SetAutoExit(autoExit)
	exec.SetInteractive(true) // Explicitly enable interactive mode

	exitCode := exec.Execute()

	// If execution failed and no automatic module handling (e.g., in terminal mode), display error
	if exitCode != 0 {
		lastError := exec.GetLastError()
		if lastError != "" {
			fmt.Fprintf(os.Stderr, "错误: %s\n", lastError)
		}
	}

	os.Exit(exitCode)
}

// Show help information
func showHelp() {
	fmt.Println("Python环境管理器")
	fmt.Println("用法:")
	fmt.Println("  程序名 [选项] [Python脚本和参数]")
	fmt.Println()
	fmt.Println("选项:")
	fmt.Println("  --list, -l              列出所有可用的Python环境")
	fmt.Println("  --set <环境名>           设置当前使用的Python环境")
	fmt.Println("  --enable <环境名>        启用指定的Python环境")
	fmt.Println("  --disable <环境名>       禁用指定的Python环境")
	fmt.Println("  --search, -s             搜索所有可用的Python环境")
	fmt.Println("  --info, -i               显示当前Python环境信息")
	fmt.Println("  --edit, -e               在记事本中编辑配置文件")
	fmt.Println("  --no-capture             不捕获输出（直接显示在控制台）")
	fmt.Println("  --no-exit                执行完成后不自动退出")
	fmt.Println("  --help, -h               显示此帮助信息")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  程序名 script.py         使用当前Python环境执行脚本")
	fmt.Println("  程序名 --set conda_0     设置使用conda_0环境")
	fmt.Println("  程序名 --list            列出所有Python环境")
	fmt.Println("  程序名 --edit            在记事本中编辑配置文件")
}

// List all Python environments
func listEnvironments(cfg *config.PythonConfig) {
	environments := cfg.GetPythonEnvironments()

	fmt.Println("可用的Python环境:")
	fmt.Println("----------------------------------------")

	currentEnv := cfg.GetCurrentEnvironment()
	for _, env := range environments {
		fmt.Printf("名称: %s\n", env.Name)
		fmt.Printf("路径: %s\n", env.Path)
		fmt.Printf("版本: %s\n", func() string {
			if env.Version == "" {
				return "未知"
			}
			return env.Version
		}())
		fmt.Printf("编码: %s\n", env.Encoding)
		fmt.Printf("平台: %s\n", func() string {
			if env.Platform == "" {
				return "windows"
			}
			return env.Platform
		}())
		fmt.Printf("状态: %s\n", func() string {
			if env.Enabled {
				return "已启用"
			}
			return "已禁用"
		}())

		// Mark current environment
		if env.Name == currentEnv.Name {
			fmt.Println("当前: 是")
		}

		fmt.Println("----------------------------------------")
	}
}

// Execute a system command
func executeSystemCommand(cmd string) error {
	// Execute command using Windows cmd
	command := exec.Command("cmd", "/C", cmd)
	return command.Run()
}
