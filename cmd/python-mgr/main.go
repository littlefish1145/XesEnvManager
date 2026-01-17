//go:build !linux && !unix
// +build !linux,!unix

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof"
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

const (
	RepoOwner  = "littlefish1145"
	RepoName   = "XesEnvManager"
	AppVersion = "stable_0.2.3"
)

var (
	httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	downloadClient = &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        16,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
		},
	}
)

type AppState struct {
	cfg *config.PythonConfig
}

func main() {
	app := &AppState{
		cfg: config.NewPythonConfig(""),
	}

	// 启用pprof分析服务器（仅在启用时）
	go func() {
		if os.Getenv("PPROF_ENABLED") == "true" {
			pprofPort := "6060"
			if envPort := os.Getenv("PPROF_PORT"); envPort != "" {
				pprofPort = envPort
			}
			log.Printf("启动pprof分析服务器在 :%s", pprofPort)
			log.Printf("访问 http://localhost:%s/debug/pprof/ 查看分析数据", pprofPort)
			log.Printf("使用 go tool pprof http://localhost:%s/debug/pprof/heap 分析内存", pprofPort)
			log.Println(http.ListenAndServe("localhost:"+pprofPort, nil))
		}
	}()

	needCreateConfig := checkNeedCreateConfig()

	if !app.cfg.LoadOrCreateConfig(needCreateConfig) {
		fmt.Fprintln(os.Stderr, "无法加载或创建配置文件")
		os.Exit(1)
	}

	hasPythonScriptForArgs := hasPythonScriptArg()

	pythonArgs, captureOutput, autoExit, showInfo, pprofWait := parseArguments(app, hasPythonScriptForArgs)
	if len(pythonArgs) == 0 && !showInfo {
		showHelp()
		return
	}

	if showInfo {
		app.cfg.DisplayCurrentEnvironment()
	}

	if len(pythonArgs) == 0 {
		if !app.cfg.GetDisableUpdateCheck() {
			checkUpdateSilent(app)
		}
		if pprofWait {
			log.Println("等待 60 秒以供 pprof 分析...")
			time.Sleep(60 * time.Second)
		}
		return
	}

	exec := executor.NewPythonExecutor(app.cfg)
	exec.SetArguments(pythonArgs)
	exec.SetCaptureOutput(captureOutput)
	exec.SetAutoExit(autoExit)
	exec.SetInteractive(true)

	exitCode := exec.Execute()

	if !app.cfg.GetDisableUpdateCheck() {
		checkUpdateSilent(app)
	}

	if pprofWait {
		log.Println("程序执行完成。等待 60 秒以供 pprof 分析...")
		time.Sleep(60 * time.Second)
	}

	if exitCode != 0 {
		lastError := exec.GetLastError()
		if lastError != "" {
			fmt.Fprintf(os.Stderr, "错误: %s\n", lastError)
		}
	}

	os.Exit(exitCode)
}

func checkNeedCreateConfig() bool {
	needCreateConfig := false
	hasPythonScript := false

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--list" || arg == "-l" {
			needCreateConfig = true
		}
		if strings.HasSuffix(arg, ".py") {
			hasPythonScript = true
		}
	}

	if hasPythonScript {
		needCreateConfig = false
	}

	return needCreateConfig
}

func hasPythonScriptArg() bool {
	for i := 1; i < len(os.Args); i++ {
		if strings.HasSuffix(os.Args[i], ".py") {
			return true
		}
	}
	return false
}

func parseArguments(appState *AppState, hasPythonScriptForArgs bool) ([]string, bool, bool, bool, bool) {
	var pythonArgs []string
	captureOutput := true
	autoExit := true
	showInfo := false
	pprofWait := false

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]

		if shouldExitImmediately(arg) {
			handleExitCommand(arg, appState)
			os.Exit(0)
		}

		if shouldHandleWithNextArg(arg) && i+1 < len(os.Args) {
			handleCommandWithArg(arg, os.Args[i+1], appState)
			os.Exit(0)
		}

		if shouldToggleFlag(arg) {
			captureOutput, autoExit, showInfo, pprofWait = updateFlags(arg, captureOutput, autoExit, showInfo, pprofWait, appState)
			continue
		}

		if isListOrSearchCommand(arg, hasPythonScriptForArgs) {
			handleListOrSearch(arg, hasPythonScriptForArgs, appState)
			os.Exit(0)
		}

		pythonArgs = append(pythonArgs, arg)
	}

	return pythonArgs, captureOutput, autoExit, showInfo, pprofWait
}

func shouldExitImmediately(arg string) bool {
	exitCommands := []string{"--help", "-h", "--update", "-U"}
	for _, cmd := range exitCommands {
		if arg == cmd {
			return true
		}
	}
	return false
}

func handleExitCommand(arg string, appState *AppState) {
	if arg == "--help" || arg == "-h" {
		showHelp()
	} else if arg == "--update" || arg == "-U" {
		updateSelf(appState)
	}
}

func shouldHandleWithNextArg(arg string) bool {
	commandsWithArg := []string{"--set", "--enable", "--disable"}
	for _, cmd := range commandsWithArg {
		if arg == cmd {
			return true
		}
	}
	return false
}

func handleCommandWithArg(arg, nextArg string, appState *AppState) {
	if arg == "--set" {
		handleSetEnv(nextArg, appState)
	} else if arg == "--enable" {
		handleToggleEnv(nextArg, true, appState)
	} else if arg == "--disable" {
		handleToggleEnv(nextArg, false, appState)
	}
}

func shouldToggleFlag(arg string) bool {
	toggleFlags := []string{"--info", "-i", "--no-capture", "--no-exit", "--disable-update", "--enable-update", "--edit", "-e", "--pprof-wait"}
	for _, flag := range toggleFlags {
		if arg == flag {
			return true
		}
	}
	return false
}

func updateFlags(arg string, captureOutput, autoExit, showInfo, pprofWait bool, appState *AppState) (bool, bool, bool, bool) {
	if arg == "--info" || arg == "-i" {
		return captureOutput, autoExit, true, pprofWait
	} else if arg == "--no-capture" {
		return false, autoExit, showInfo, pprofWait
	} else if arg == "--no-exit" {
		return captureOutput, false, showInfo, pprofWait
	} else if arg == "--pprof-wait" {
		return captureOutput, autoExit, showInfo, true
	} else if arg == "--disable-update" {
		handleDisableUpdate(appState)
		os.Exit(0)
	} else if arg == "--enable-update" {
		handleEnableUpdate(appState)
		os.Exit(0)
	} else if arg == "--edit" || arg == "-e" {
		handleEditConfig(appState)
		os.Exit(0)
	}
	return captureOutput, autoExit, showInfo, pprofWait
}

func isListOrSearchCommand(arg string, hasPythonScriptForArgs bool) bool {
	listSearchCommands := []string{"--list", "-l", "--search", "-s"}
	for _, cmd := range listSearchCommands {
		if arg == cmd {
			return true
		}
	}
	return false
}

func handleListOrSearch(arg string, hasPythonScriptForArgs bool, appState *AppState) {
	if arg == "--list" || arg == "-l" {
		if hasPythonScriptForArgs {
			return
		}
		appState.cfg.SearchPythonEnvironments()
		fmt.Println("搜索完成，已更新配置文件")
		listEnvironments(appState.cfg)
	} else if arg == "--search" || arg == "-s" {
		if hasPythonScriptForArgs {
			return
		}
		fmt.Println("正在搜索Python环境...")
		appState.cfg.SearchPythonEnvironments()
		fmt.Println("搜索完成，已更新配置文件")
		listEnvironments(appState.cfg)
	}
}

func handleSetEnv(envName string, appState *AppState) {
	i := 0
	for i < len(os.Args) {
		if os.Args[i] == "--set" {
			i++
			break
		}
		i++
	}
	if appState.cfg.SetCurrentEnvironment(envName) {
		fmt.Printf("已设置当前Python环境为: %s\n", envName)
	} else {
		fmt.Fprintf(os.Stderr, "设置Python环境失败: %s\n", envName)
		os.Exit(1)
	}
}

func handleToggleEnv(envName string, enabled bool, appState *AppState) {
	if appState.cfg.ToggleEnvironment(envName, enabled) {
		action := "禁用"
		if enabled {
			action = "启用"
		}
		fmt.Printf("已%sPython环境: %s\n", action, envName)
	} else {
		action := "禁用"
		if enabled {
			action = "启用"
		}
		fmt.Fprintf(os.Stderr, "%sPython环境失败: %s\n", action, envName)
		os.Exit(1)
	}
}

func handleDisableUpdate(appState *AppState) {
	if appState.cfg.SetDisableUpdateCheck(true) {
		fmt.Println("已永久关闭运行时的更新提示。")
	} else {
		fmt.Println("设置失败。")
	}
}

func handleEnableUpdate(appState *AppState) {
	if appState.cfg.SetDisableUpdateCheck(false) {
		fmt.Println("已开启运行时的更新提示。")
	} else {
		fmt.Println("设置失败。")
	}
}

func handleEditConfig(appState *AppState) {
	configPath := appState.cfg.GetConfigPath()

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "配置文件不存在: %s\n", configPath)
		return
	}

	cmd := exec.Command("notepad.exe", configPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		fmt.Printf("已在记事本中打开配置文件: %s\n", configPath)
	} else {
		fmt.Fprintf(os.Stderr, "打开配置文件失败: 无法启动记事本。错误详情: %v\n", err)
		fmt.Fprintf(os.Stderr, "请尝试手动打开配置文件: %s\n", configPath)
	}
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
func updateSelf(appState *AppState) {
	fmt.Printf("当前版本: %s\n", AppVersion)
	fmt.Println("正在获取 GitHub Release 信息...")

	resp, err := httpClient.Get(fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", RepoOwner, RepoName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "检查更新失败: 无法连接到GitHub服务器，请检查网络连接。错误详情: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "检查更新失败: GitHub API返回错误状态码 %d。可能是API限流或服务暂时不可用。\n", resp.StatusCode)
		return
	}

	var releases []ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		fmt.Fprintf(os.Stderr, "解析更新信息失败: GitHub返回的数据格式不正确。错误详情: %v\n", err)
		return
	}

	if len(releases) == 0 {
		fmt.Println("未发现任何发布版本。")
		return
	}

	stableRelease, previewRelease := findReleases(releases)
	displayUpdateMenu(stableRelease, previewRelease, appState)
}

func findReleases(releases []ReleaseInfo) (*ReleaseInfo, *ReleaseInfo) {
	stableRelease, previewRelease := findReleasesByPrefix(releases)

	if stableRelease == nil {
		stableRelease = findFallbackStableRelease(releases)
	}
	if previewRelease == nil {
		previewRelease = findFallbackPreviewRelease(releases)
	}

	return stableRelease, previewRelease
}

func findReleasesByPrefix(releases []ReleaseInfo) (*ReleaseInfo, *ReleaseInfo) {
	var stableRelease *ReleaseInfo
	var previewRelease *ReleaseInfo

	for i := range releases {
		rel := &releases[i]
		if strings.HasPrefix(rel.TagName, "stable_") && stableRelease == nil {
			stableRelease = rel
		} else if strings.HasPrefix(rel.TagName, "pre_") && previewRelease == nil {
			previewRelease = rel
		}

		if stableRelease != nil && previewRelease != nil {
			break
		}
	}

	return stableRelease, previewRelease
}

func findFallbackStableRelease(releases []ReleaseInfo) *ReleaseInfo {
	for i := range releases {
		if !releases[i].Prerelease && strings.Contains(strings.ToLower(releases[i].TagName), "release") {
			return &releases[i]
		}
	}
	return nil
}

func findFallbackPreviewRelease(releases []ReleaseInfo) *ReleaseInfo {
	for i := range releases {
		if releases[i].Prerelease || strings.Contains(strings.ToLower(releases[i].TagName), "main") {
			return &releases[i]
		}
	}
	return nil
}

func displayUpdateMenu(stableRelease, previewRelease *ReleaseInfo, appState *AppState) {
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

	autoUpdateStatus := "已关闭"
	if appState.cfg.GetAutoUpdate() {
		autoUpdateStatus = "已开启"
	}
	disableUpdateStatus := "正常提醒"
	if appState.cfg.GetDisableUpdateCheck() {
		disableUpdateStatus = "已屏蔽"
	}

	fmt.Printf("[A] 自动更新: %s (切换)\n", autoUpdateStatus)
	fmt.Printf("[N] 更新提醒: %s (切换)\n", disableUpdateStatus)
	fmt.Println("[Q] 取消更新")

	fmt.Print("\n请输入选项 (1/2/A/N/Q): ")
	scanner := bufio.NewScanner(os.Stdin)
	defer func() {
		if scanner != nil {
			scanner = nil
		}
	}()
	var choice string
	if scanner.Scan() {
		choice = strings.TrimSpace(strings.ToUpper(scanner.Text()))
	}

	handleUpdateChoice(choice, stableRelease, previewRelease, appState)
}

func handleUpdateChoice(choice string, stableRelease, previewRelease *ReleaseInfo, appState *AppState) {
	if isSettingsChoice(choice) {
		handleSettingsChoice(choice, appState)
		return
	}

	if isExitChoice(choice) {
		fmt.Println("已退出更新菜单。")
		return
	}

	selectedRelease := selectRelease(choice, stableRelease, previewRelease)
	if selectedRelease == nil {
		return
	}

	if isCurrentVersion(selectedRelease) {
		return
	}

	confirmAndUpdate(selectedRelease)
}

func isSettingsChoice(choice string) bool {
	settingsChoices := []string{"A", "N"}
	for _, c := range settingsChoices {
		if choice == c {
			return true
		}
	}
	return false
}

func handleSettingsChoice(choice string, appState *AppState) {
	if choice == "A" {
		toggleAutoUpdate(appState)
	} else if choice == "N" {
		toggleUpdateReminder(appState)
	}
}

func toggleAutoUpdate(appState *AppState) {
	newVal := !appState.cfg.GetAutoUpdate()
	appState.cfg.SetAutoUpdate(newVal)
	if newVal {
		fmt.Println("自动更新已开启。")
	} else {
		fmt.Println("自动更新已关闭。")
	}
}

func toggleUpdateReminder(appState *AppState) {
	newVal := !appState.cfg.GetDisableUpdateCheck()
	appState.cfg.SetDisableUpdateCheck(newVal)
	if newVal {
		fmt.Println("更新提醒已屏蔽。")
	} else {
		fmt.Println("更新提醒已恢复正常。")
	}
}

func isExitChoice(choice string) bool {
	return choice == "Q"
}

func selectRelease(choice string, stableRelease, previewRelease *ReleaseInfo) *ReleaseInfo {
	if choice == "1" {
		return stableRelease
	} else if choice == "2" {
		return previewRelease
	} else {
		fmt.Println("无效选项，已退出。")
		return nil
	}
}

func isCurrentVersion(release *ReleaseInfo) bool {
	if release == nil {
		fmt.Println("所选版本不存在。")
		return true
	}

	if release.TagName == AppVersion {
		fmt.Printf("当前已是 %s 版本，无需更新。\n", release.TagName)
		return true
	}

	return false
}

func confirmAndUpdate(release *ReleaseInfo) {
	fmt.Printf("\n准备更新至: %s\n", release.TagName)
	fmt.Println("更新内容:")
	fmt.Println("----------------------------------------")
	fmt.Println(release.Body)
	fmt.Println("----------------------------------------")
	fmt.Print("\n是否确认下载并替换当前程序？(y/n): ")

	scanner := bufio.NewScanner(os.Stdin)
	defer func() {
		if scanner != nil {
			scanner = nil
		}
	}()
	if scanner.Scan() {
		confirm := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if confirm != "y" && confirm != "yes" {
			fmt.Println("已取消更新。")
			return
		}
	}

	downloadURL := findDownloadURL(release)
	if downloadURL == "" {
		fmt.Println("该版本未找到任何可下载的资源。")
		return
	}

	fmt.Println("正在下载更新...")
	if err := downloadAndReplace(downloadURL); err != nil {
		fmt.Printf("更新失败: %v\n", err)
		return
	}

	fmt.Println("\n更新成功！请重新启动程序。")
	os.Exit(0)
}

func findDownloadURL(release *ReleaseInfo) string {
	var downloadURL string
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, runtime.GOOS) && (strings.Contains(name, runtime.GOARCH) || (runtime.GOARCH == "amd64" && strings.Contains(name, "x64"))) {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		for _, asset := range release.Assets {
			name := strings.ToLower(asset.Name)
			if ext != "" && strings.HasSuffix(name, ext) {
				downloadURL = asset.BrowserDownloadURL
				break
			}
		}
	}

	if downloadURL == "" {
		if len(release.Assets) > 0 {
			downloadURL = release.Assets[0].BrowserDownloadURL
		}
	}

	return downloadURL
}

// downloadAndReplace downloads the new binary and replaces the current one
func downloadAndReplace(url string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取程序路径: %v", err)
	}

	tmpPath := exePath + ".tmp"
	oldPath := exePath + ".old"

	contentLength, err := getFileSize(url)
	if err != nil {
		return err
	}

	if err := downloadFile(url, contentLength, tmpPath); err != nil {
		return err
	}

	return replaceExecutable(exePath, tmpPath, oldPath)
}

func getFileSize(url string) (int64, error) {
	respHead, err := httpClient.Head(url)
	if err != nil {
		return 0, fmt.Errorf("连接失败: %v", err)
	}
	defer respHead.Body.Close()
	if respHead.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("连接失败，状态码: %d", respHead.StatusCode)
	}
	return respHead.ContentLength, nil
}

func downloadFile(url string, contentLength int64, outputPath string) error {
	numThreads := 16
	chunkSize := contentLength / int64(numThreads)
	if chunkSize == 0 {
		numThreads = 1
		chunkSize = contentLength
	}

	var wg sync.WaitGroup
	errors := make(chan error, numThreads)
	done := make(chan struct{})

	// Create a context that is cancelled when any error occurs or when done
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var downloaded int64
	var downloadedMutex sync.Mutex

	// Create a dedicated progress goroutine to avoid race conditions
	progressChan := make(chan int64, 100)
	go func() {
		defer close(progressChan)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				downloadedMutex.Lock()
				progressChan <- downloaded
				downloadedMutex.Unlock()
			}
		}
	}()

	tempDir := filepath.Join(os.TempDir(), "python-mgr-update")
	_ = os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	fmt.Print("\r正在多线程下载: [....................] 0%")

	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		start := int64(i) * chunkSize
		end := start + chunkSize - 1
		if i == numThreads-1 {
			end = contentLength - 1
		}

		go func(index int, start, end int64) {
			defer wg.Done()
			downloadChunk(ctx, index, start, end, url, tempDir, &downloaded, &downloadedMutex, contentLength, errors)
		}(i, start, end)
	}

	// Monitor progress and errors
	completed := false
	for !completed {
		select {
		case err := <-errors:
			cancel()
			close(done)
			return err
		case progress := <-progressChan:
			if contentLength > 0 {
				percent := float64(progress) / float64(contentLength) * 100
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
			}
		case <-done:
			completed = true
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	wg.Wait()
	close(errors)
	close(done)

	fmt.Printf("\r正在多线程下载: [%s] 100.0%%", "####################")
	fmt.Println("\n正在合并文件...")

	return mergeChunks(tempDir, outputPath, numThreads)
}

func downloadChunk(ctx context.Context, index int, start, end int64, url, tempDir string, downloaded *int64, downloadedMutex *sync.Mutex, contentLength int64, errors chan error) {
	chunkCtx, chunkCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer chunkCancel()

	chunkPath := filepath.Join(tempDir, fmt.Sprintf("chunk_%d", index))
	var currentStart int64 = start

	if info, err := os.Stat(chunkPath); err == nil {
		currentStart += info.Size()
	}

	if currentStart > end {
		downloadedMutex.Lock()
		*downloaded += (end - start + 1)
		downloadedMutex.Unlock()
		return
	}

	select {
	case <-ctx.Done():
		return
	default:
	}

	req, err := http.NewRequestWithContext(chunkCtx, "GET", url, nil)
	if err != nil {
		select {
		case errors <- fmt.Errorf("创建请求失败: %v", err):
		case <-ctx.Done():
		}
		return
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", currentStart, end))

	resp, err := downloadClient.Do(req)
	if err != nil {
		select {
		case errors <- fmt.Errorf("下载分片 %d 失败: %v", index, err):
		case <-ctx.Done():
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		select {
		case errors <- fmt.Errorf("下载分片 %d 失败: 服务器返回状态码 %d", index, resp.StatusCode):
		case <-ctx.Done():
		}
		return
	}

	f, err := os.OpenFile(chunkPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		select {
		case errors <- fmt.Errorf("创建分片文件 %d 失败: %v", index, err):
		case <-ctx.Done():
		}
		return
	}
	defer f.Close()

	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, err := f.Write(buffer[:n]); err != nil {
				select {
				case errors <- fmt.Errorf("写入分片文件 %d 失败: %v", index, err):
				case <-ctx.Done():
				}
				return
			}
			downloadedMutex.Lock()
			*downloaded += int64(n)
			downloadedMutex.Unlock()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			select {
			case errors <- fmt.Errorf("读取分片 %d 数据失败: %v", index, err):
			case <-ctx.Done():
			}
			return
		}
	}
}

func mergeChunks(tempDir, outputPath string, numThreads int) error {
	finalOut, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %v", err)
	}
	defer func() {
		if finalOut != nil {
			finalOut.Close()
		}
	}()

	// Use a smaller buffer size for better memory usage
	buffer := make([]byte, 16*1024)

	for i := 0; i < numThreads; i++ {
		chunkPath := filepath.Join(tempDir, fmt.Sprintf("chunk_%d", i))
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			return fmt.Errorf("打开分片文件 %d 失败: %v", i, err)
		}

		_, err = io.CopyBuffer(finalOut, chunkFile, buffer)
		chunkFile.Close() // Close immediately after use
		if err != nil {
			return fmt.Errorf("合并分片文件 %d 失败: %v", i, err)
		}
	}

	// Ensure all data is written to disk
	if err := finalOut.Sync(); err != nil {
		return fmt.Errorf("同步文件到磁盘失败: %v", err)
	}

	// Explicitly close before returning
	finalOut.Close()
	finalOut = nil

	return nil
}

func replaceExecutable(exePath, tmpPath, oldPath string) error {
	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Remove(oldPath); err != nil {
			return fmt.Errorf("删除旧备份文件失败: %v", err)
		}
	}

	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("备份当前程序失败: %v", err)
	}

	if err := os.Rename(tmpPath, exePath); err != nil {
		if restoreErr := os.Rename(oldPath, exePath); restoreErr != nil {
			return fmt.Errorf("替换程序失败: %v，且恢复备份也失败: %v", err, restoreErr)
		}
		return fmt.Errorf("替换程序失败: %v，已恢复原程序", err)
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
	fmt.Println("  --pprof-wait             执行完成后等待一段时间以供 pprof 分析")
	fmt.Println("  --help, -h               显示此帮助信息")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  程序名 script.py         使用当前Python环境执行脚本")
	fmt.Println("  程序名 --set conda_0     设置使用conda_0环境")
	fmt.Println("  程序名 --list            列出所有Python环境")
	fmt.Println("  程序名 --edit            在记事本中编辑配置文件")
}

func doAutoUpdate(rel *ReleaseInfo) {
	if rel == nil {
		fmt.Fprintf(os.Stderr, "自动更新失败: 版本信息为空\n")
		return
	}

	downloadURL := findDownloadURL(rel)
	if downloadURL == "" {
		fmt.Fprintf(os.Stderr, "自动更新失败: 未找到适用于当前系统的下载链接\n")
		return
	}

	fmt.Println("正在下载更新...")
	if err := downloadAndReplace(downloadURL); err != nil {
		fmt.Fprintf(os.Stderr, "自动更新失败: %v\n", err)
		fmt.Fprintf(os.Stderr, "请稍后手动更新或检查网络连接\n")
		return
	}

	fmt.Println("\n自动更新成功！请重新启动程序。")
	os.Exit(0)
}

func checkUpdateSilent(appState *AppState) {
	resp, err := httpClient.Get(fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", RepoOwner, RepoName))
	if err != nil {
		return
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

	current := parseVersion(AppVersion)
	latestStable, latestPre := findLatestReleases(releases)

	if appState.cfg.GetAutoUpdate() {
		if latestStable != nil && isNewerThan(latestStable.TagName, AppVersion) {
			fmt.Printf("检测到新版本 %s，正在自动更新...\n", latestStable.TagName)
			// Close body before potential exit in doAutoUpdate
			resp.Body.Close()
			doAutoUpdate(latestStable)
			return
		}
	}

	displayUpdateHint(current, latestStable, latestPre, appState)
}

func findLatestReleases(releases []ReleaseInfo) (*ReleaseInfo, *ReleaseInfo) {
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

	return latestStable, latestPre
}

func displayUpdateHint(current VersionInfo, latestStable, latestPre *ReleaseInfo, appState *AppState) {
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
		defer func() {
			if scanner != nil {
				scanner = nil
			}
		}()
		if scanner.Scan() {
			choice := strings.TrimSpace(strings.ToUpper(scanner.Text()))
			handleUpdateHintChoice(choice, appState)
		}
	}
}

func handleUpdateHintChoice(choice string, appState *AppState) {
	switch choice {
	case "1":
		updateSelf(appState)
	case "A":
		newVal := !appState.cfg.GetAutoUpdate()
		appState.cfg.SetAutoUpdate(newVal)
		if newVal {
			fmt.Println("已开启自动更新。")
		} else {
			fmt.Println("已关闭自动更新。")
		}
	case "N":
		newVal := !appState.cfg.GetDisableUpdateCheck()
		appState.cfg.SetDisableUpdateCheck(newVal)
		if newVal {
			fmt.Println("已屏蔽更新提醒。")
		} else {
			fmt.Println("已恢复更新提醒。")
		}
	default:
		fmt.Println("好的，稍后将再次提醒您。")
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
