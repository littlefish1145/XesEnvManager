//go:build !linux && !unix
// +build !linux,!unix

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"python-manager/pkg/config"
	"python-manager/pkg/executor"
)

// Global variables
var (
	cfg        *config.PythonConfig
	AppVersion = "stable_0.2.3"
)

const (
	RepoOwner = "littlefish1145"
	RepoName  = "XesEnvManager"
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
		} else if arg == "--update" || arg == "-U" {
			updateSelf()
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
		} else if arg == "--disable-update" {
			if cfg.SetDisableUpdateCheck(true) {
				fmt.Println("已永久关闭运行时的更新提示。")
			} else {
				fmt.Println("设置失败。")
			}
			return
		} else if arg == "--enable-update" {
			if cfg.SetDisableUpdateCheck(false) {
				fmt.Println("已开启运行时的更新提示。")
			} else {
				fmt.Println("设置失败。")
			}
			return
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
		// Also check update when just showing info
		if !cfg.GetDisableUpdateCheck() {
			checkUpdateSilent()
		}
		return
	}

	// Create executor and execute Python script
	exec := executor.NewPythonExecutor(cfg)
	exec.SetArguments(pythonArgs)
	exec.SetCaptureOutput(captureOutput)
	exec.SetAutoExit(autoExit)
	exec.SetInteractive(true) // Explicitly enable interactive mode

	exitCode := exec.Execute()

	// Check for updates if not disabled, AFTER execution
	if !cfg.GetDisableUpdateCheck() {
		checkUpdateSilent()
	}

	// If execution failed and no automatic module handling (e.g., in terminal mode), display error
	if exitCode != 0 {
		lastError := exec.GetLastError()
		if lastError != "" {
			fmt.Fprintf(os.Stderr, "错误: %s\n", lastError)
		}
	}

	os.Exit(exitCode)
}

type ReleaseInfo struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// VersionInfo represents a parsed version string
type VersionInfo struct {
	Prefix string
	Major  int
	Minor  int
	Patch  int
}

// parseVersion parses a version string like "stable_1.2.3" or "pre_1.2.3"
func parseVersion(v string) VersionInfo {
	info := VersionInfo{Prefix: "stable", Major: 0, Minor: 0, Patch: 0}

	// Split by underscore first to get prefix and version numbers
	parts := strings.Split(v, "_")
	if len(parts) < 2 {
		return info
	}

	info.Prefix = parts[0]

	// Split the second part by dots to get major, minor, patch
	versionParts := strings.Split(parts[1], ".")
	if len(versionParts) > 0 {
		info.Major, _ = strconv.Atoi(versionParts[0])
	}
	if len(versionParts) > 1 {
		info.Minor, _ = strconv.Atoi(versionParts[1])
	}
	if len(versionParts) > 2 {
		info.Patch, _ = strconv.Atoi(versionParts[2])
	}

	return info
}

// isNewerThan compares two version strings and returns true if v1 is newer than v2
func isNewerThan(v1Str, v2Str string) bool {
	v1 := parseVersion(v1Str)
	v2 := parseVersion(v2Str)

	// Compare major version
	if v1.Major > v2.Major {
		return true
	}
	if v1.Major < v2.Major {
		return false
	}

	// Major versions are equal, compare minor
	if v1.Minor > v2.Minor {
		return true
	}
	if v1.Minor < v2.Minor {
		return false
	}

	// Minor versions are equal, compare patch
	if v1.Patch > v2.Patch {
		return true
	}
	if v1.Patch < v2.Patch {
		return false
	}

	// All version numbers are equal, compare prefix
	// stable > pre
	if v1.Prefix == "stable" && v2.Prefix == "pre" {
		return true
	}

	return false
}

// updateSelf updates the application from GitHub Releases
func updateSelf() {
	fmt.Printf("当前版本: %s\n", AppVersion)
	fmt.Println("正在获取 GitHub Release 信息...")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", RepoOwner, RepoName))
	if err != nil {
		fmt.Printf("检查更新失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("检查更新失败，状态码: %d\n", resp.StatusCode)
		return
	}

	var releases []ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		fmt.Printf("解析更新信息失败: %v\n", err)
		return
	}

	if len(releases) == 0 {
		fmt.Println("未发现任何发布版本。")
		return
	}

	var stableRelease *ReleaseInfo
	var previewRelease *ReleaseInfo

	for i := range releases {
		rel := &releases[i]
		// Identify based on new tag format: prefix_x.x.x
		if strings.HasPrefix(rel.TagName, "stable_") && stableRelease == nil {
			stableRelease = rel
		} else if strings.HasPrefix(rel.TagName, "pre_") && previewRelease == nil {
			previewRelease = rel
		}

		if stableRelease != nil && previewRelease != nil {
			break
		}
	}

	// Fallback to older identification or generic prerelease flag if still nil
	if stableRelease == nil {
		for i := range releases {
			if !releases[i].Prerelease && strings.Contains(strings.ToLower(releases[i].TagName), "release") {
				stableRelease = &releases[i]
				break
			}
		}
	}
	if previewRelease == nil {
		for i := range releases {
			if releases[i].Prerelease || strings.Contains(strings.ToLower(releases[i].TagName), "main") {
				previewRelease = &releases[i]
				break
			}
		}
	}

	fmt.Println("\n请选择更新版本:")
	if stableRelease != nil {
		fmt.Printf("[1] 正式发布 (Stable): %s\n", stableRelease.TagName)
	} else {
		fmt.Println("[1] 正式发布 (Stable): 未找到")
	}

	if previewRelease != nil {
		fmt.Printf("[2] 预览发布 (Preview): %s\n", previewRelease.TagName)
	} else {
		fmt.Println("[2] 预览发布 (Preview): 未找到")
	}

	// Add settings options
	autoUpdateStatus := "已关闭"
	if cfg.GetAutoUpdate() {
		autoUpdateStatus = "已开启"
	}
	disableUpdateStatus := "正常提醒"
	if cfg.GetDisableUpdateCheck() {
		disableUpdateStatus = "已屏蔽"
	}

	fmt.Printf("[A] 自动更新: %s (切换)\n", autoUpdateStatus)
	fmt.Printf("[N] 更新提醒: %s (切换)\n", disableUpdateStatus)
	fmt.Println("[Q] 取消更新")

	fmt.Print("\n请输入选项 (1/2/A/N/Q): ")
	scanner := bufio.NewScanner(os.Stdin)
	var choice string
	if scanner.Scan() {
		choice = strings.TrimSpace(strings.ToUpper(scanner.Text()))
	}

	if choice == "A" {
		newVal := !cfg.GetAutoUpdate()
		cfg.SetAutoUpdate(newVal)
		if newVal {
			fmt.Println("自动更新已开启。")
		} else {
			fmt.Println("自动更新已关闭。")
		}
		return
	}
	if choice == "N" {
		newVal := !cfg.GetDisableUpdateCheck()
		cfg.SetDisableUpdateCheck(newVal)
		if newVal {
			fmt.Println("更新提醒已屏蔽。")
		} else {
			fmt.Println("更新提醒已恢复正常。")
		}
		return
	}
	if choice == "Q" {
		fmt.Println("已退出更新菜单。")
		return
	}

	var selectedRelease *ReleaseInfo
	if choice == "1" {
		selectedRelease = stableRelease
	} else if choice == "2" {
		selectedRelease = previewRelease
	} else {
		fmt.Println("无效选项，已退出。")
		return
	}

	if selectedRelease == nil {
		fmt.Println("所选版本不存在。")
		return
	}

	if selectedRelease.TagName == AppVersion {
		fmt.Printf("当前已是 %s 版本，无需更新。\n", selectedRelease.TagName)
		return
	}

	fmt.Printf("\n准备更新至: %s\n", selectedRelease.TagName)
	fmt.Println("更新内容:")
	fmt.Println("----------------------------------------")
	fmt.Println(selectedRelease.Body)
	fmt.Println("----------------------------------------")
	fmt.Print("\n是否确认下载并替换当前程序？(y/n): ")

	if scanner.Scan() {
		confirm := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if confirm != "y" && confirm != "yes" {
			fmt.Println("已取消更新。")
			return
		}
	}

	// Find suitable asset for current platform
	var downloadURL string
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	for _, asset := range selectedRelease.Assets {
		name := strings.ToLower(asset.Name)
		// Match OS and Arch
		if strings.Contains(name, runtime.GOOS) && (strings.Contains(name, runtime.GOARCH) || (runtime.GOARCH == "amd64" && strings.Contains(name, "x64"))) {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		// Fallback for simple naming
		for _, asset := range selectedRelease.Assets {
			name := strings.ToLower(asset.Name)
			if ext != "" && strings.HasSuffix(name, ext) {
				downloadURL = asset.BrowserDownloadURL
				break
			}
		}
	}

	if downloadURL == "" {
		if len(selectedRelease.Assets) > 0 {
			downloadURL = selectedRelease.Assets[0].BrowserDownloadURL
		} else {
			fmt.Println("该版本未找到任何可下载的资源。")
			return
		}
	}

	fmt.Println("正在下载更新...")
	if err := downloadAndReplace(downloadURL); err != nil {
		fmt.Printf("更新失败: %v\n", err)
		return
	}

	fmt.Println("\n更新成功！请重新启动程序。")
	os.Exit(0)
}

// downloadAndReplace downloads the new binary and replaces the current one
func downloadAndReplace(url string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取程序路径: %v", err)
	}

	tmpPath := exePath + ".tmp"
	oldPath := exePath + ".old"

	// Create request to get file size
	respHead, err := http.Head(url)
	if err != nil {
		return fmt.Errorf("连接失败: %v", err)
	}
	if respHead.StatusCode != http.StatusOK {
		return fmt.Errorf("连接失败，状态码: %d", respHead.StatusCode)
	}
	contentLength := respHead.ContentLength

	// Support 16 threads
	numThreads := 16
	chunkSize := contentLength / int64(numThreads)
	if chunkSize == 0 {
		numThreads = 1
		chunkSize = contentLength
	}

	var wg sync.WaitGroup
	errors := make(chan error, numThreads)
	var downloaded int64
	var downloadedMutex sync.Mutex

	// Create temporary directory for chunks
	tempDir := filepath.Join(os.TempDir(), "python-mgr-update")
	os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	fmt.Print("\r正在多线程下载: [....................] 0%")

	lastUpdate := time.Now()

	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		start := int64(i) * chunkSize
		end := start + chunkSize - 1
		if i == numThreads-1 {
			end = contentLength - 1
		}

		go func(index int, start, end int64) {
			defer wg.Done()

			chunkPath := filepath.Join(tempDir, fmt.Sprintf("chunk_%d", index))
			var currentStart int64 = start

			// Check for existing chunk for resume
			if info, err := os.Stat(chunkPath); err == nil {
				currentStart += info.Size()
			}

			if currentStart > end {
				downloadedMutex.Lock()
				downloaded += (end - start + 1)
				downloadedMutex.Unlock()
				return
			}

			req, _ := http.NewRequest("GET", url, nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", currentStart, end))

			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				errors <- err
				return
			}
			defer resp.Body.Close()

			// Append to chunk file
			f, err := os.OpenFile(chunkPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				errors <- err
				return
			}
			defer f.Close()

			buffer := make([]byte, 32*1024)
			for {
				n, err := resp.Body.Read(buffer)
				if n > 0 {
					f.Write(buffer[:n])
					downloadedMutex.Lock()
					downloaded += int64(n)

					// Update progress bar
					if time.Since(lastUpdate) > 100*time.Millisecond {
						lastUpdate = time.Now()
						percent := float64(downloaded) / float64(contentLength) * 100
						bars := int(percent / 5)
						barStr := ""
						for b := 0; b < 20; b++ {
							if b < bars {
								barStr += "#"
							} else {
								barStr += "."
							}
						}
						fmt.Printf("\r正在多线程下载: [%s] %.1f%%", barStr, percent)
						os.Stdout.Sync()
					}
					downloadedMutex.Unlock()
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					errors <- err
					return
				}
			}
		}(i, start, end)
	}

	wg.Wait()
	close(errors)
	if len(errors) > 0 {
		return <-errors
	}

	fmt.Printf("\r正在多线程下载: [%s] 100.0%%", "####################")
	fmt.Println("\n正在合并文件...")

	// Merge chunks
	finalOut, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("合并失败: %v", err)
	}

	for i := 0; i < numThreads; i++ {
		chunkPath := filepath.Join(tempDir, fmt.Sprintf("chunk_%d", i))
		chunkData, err := os.ReadFile(chunkPath)
		if err != nil {
			finalOut.Close()
			return fmt.Errorf("读取分片失败: %v", err)
		}
		finalOut.Write(chunkData)
	}
	finalOut.Close()

	// Windows specific: rename current exe to .old, then rename .tmp to current exe
	if _, err := os.Stat(oldPath); err == nil {
		os.Remove(oldPath)
	}

	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("备份旧程序失败: %v", err)
	}

	if err := os.Rename(tmpPath, exePath); err != nil {
		os.Rename(oldPath, exePath)
		return fmt.Errorf("无法替换程序文件: %v", err)
	}

	return nil
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
	fmt.Println("  --update, -U             从 Github 自动更新程序")
	fmt.Println("  --disable-update         关闭运行时的自动更新检查提示")
	fmt.Println("  --enable-update          开启运行时的自动更新检查提示")
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

func doAutoUpdate(rel *ReleaseInfo) {
	// Find suitable asset for current platform
	var downloadURL string
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	for _, asset := range rel.Assets {
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, runtime.GOOS) && (strings.Contains(name, runtime.GOARCH) || (runtime.GOARCH == "amd64" && strings.Contains(name, "x64"))) {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		for _, asset := range rel.Assets {
			name := strings.ToLower(asset.Name)
			if ext != "" && strings.HasSuffix(name, ext) {
				downloadURL = asset.BrowserDownloadURL
				break
			}
		}
	}

	if downloadURL == "" {
		if len(rel.Assets) > 0 {
			downloadURL = rel.Assets[0].BrowserDownloadURL
		} else {
			return
		}
	}

	if err := downloadAndReplace(downloadURL); err != nil {
		fmt.Printf("自动更新失败: %v\n", err)
		return
	}

	fmt.Println("\n自动更新成功！请重新启动程序。")
	os.Exit(0)
}

// checkUpdateSilent checks for updates silently and prints a hint if available
func checkUpdateSilent() {
	// Use a short timeout to not block too long
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", RepoOwner, RepoName))
	if err != nil {
		return // Silently ignore errors
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var releases []ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return
	}

	if len(releases) == 0 {
		return
	}

	// Current version info
	current := parseVersion(AppVersion)

	// Check for updates
	var latestStable *ReleaseInfo
	var latestPre *ReleaseInfo

	for i := range releases {
		rel := &releases[i]
		if strings.HasPrefix(rel.TagName, "stable_") && latestStable == nil {
			latestStable = rel
		} else if strings.HasPrefix(rel.TagName, "pre_") && latestPre == nil {
			latestPre = rel
		}
		if latestStable != nil && latestPre != nil {
			break
		}
	}

	// If auto-update is enabled, update without asking
	if cfg.GetAutoUpdate() {
		if latestStable != nil && isNewerThan(latestStable.TagName, AppVersion) {
			fmt.Printf("检测到新版本 %s，正在自动更新...\n", latestStable.TagName)
			// Call updateSelf logic or a direct download
			doAutoUpdate(latestStable)
			return
		}
	}

	var hint string
	if latestStable != nil && isNewerThan(latestStable.TagName, AppVersion) {
		hint = fmt.Sprintf("\n[!] 发现新的正式版本: %s (当前版本: %s)", latestStable.TagName, AppVersion)
	} else if current.Prefix == "pre" && latestPre != nil && isNewerThan(latestPre.TagName, AppVersion) {
		hint = fmt.Sprintf("\n[!] 发现新的预览版本: %s (当前版本: %s)", latestPre.TagName, AppVersion)
	}

	if hint != "" {
		fmt.Println(hint)
		fmt.Println("1. 立即更新")
		fmt.Println("A. 开启/关闭自动更新")
		fmt.Println("N. 屏蔽/恢复更新提醒")
		fmt.Println("Q. 稍后提醒")
		fmt.Print("\n请选择 (1/A/N/Q): ")

		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			choice := strings.TrimSpace(strings.ToUpper(scanner.Text()))
			switch choice {
			case "1":
				updateSelf()
			case "A":
				newVal := !cfg.GetAutoUpdate()
				cfg.SetAutoUpdate(newVal)
				if newVal {
					fmt.Println("已开启自动更新。")
				} else {
					fmt.Println("已关闭自动更新。")
				}
			case "N":
				newVal := !cfg.GetDisableUpdateCheck()
				cfg.SetDisableUpdateCheck(newVal)
				if newVal {
					fmt.Println("已屏蔽更新提醒。")
				} else {
					fmt.Println("已恢复更新提醒。")
				}
			default:
				fmt.Println("好的，稍后将再次提醒您。")
			}
		}
	}
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
