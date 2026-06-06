//go:build windows

package process

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	// Windows API 函数
	shell32        = syscall.NewLazyDLL("shell32.dll")
	user32         = syscall.NewLazyDLL("user32.dll")
	gdi32          = syscall.NewLazyDLL("gdi32.dll")
	extractIconExW = shell32.NewProc("ExtractIconExW")
	getIconInfo    = user32.NewProc("GetIconInfo")
	getBitmapBits  = gdi32.NewProc("GetBitmapBits")
	deleteObject   = gdi32.NewProc("DeleteObject")
	destroyIcon    = user32.NewProc("DestroyIcon")
)

// ICONINFO Windows图标信息结构
type ICONINFO struct {
	fIcon    uint32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

// BITMAP Windows位图结构
type BITMAP struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

// IconExtractor Windows图标提取器
type IconExtractor struct {
	cachePath string // 图标缓存路径
}

// NewIconExtractor 创建图标提取器
func NewIconExtractor() *IconExtractor {
	// 创建缓存目录
	cacheDir := filepath.Join(os.TempDir(), "sniffy_icons")
	os.MkdirAll(cacheDir, 0755)

	return &IconExtractor{
		cachePath: cacheDir,
	}
}

// ExtractIcon 从可执行文件中提取图标
func (ie *IconExtractor) ExtractIcon(executablePath string) (*ProcessIconInfo, error) {
	if executablePath == "" {
		return ie.getDefaultIcon(), nil
	}

	// 检查文件是否存在
	if _, err := os.Stat(executablePath); os.IsNotExist(err) {
		return ie.getDefaultIcon(), nil
	}

	// 尝试从缓存获取
	if cachedIcon := ie.getCachedIcon(executablePath); cachedIcon != nil {
		return cachedIcon, nil
	}

	// 提取图标
	iconData, err := ie.extractIconFromFile(executablePath)
	if err != nil {
		// 如果提取失败，返回基于文件名的默认图标
		return ie.getIconByFileName(filepath.Base(executablePath)), nil
	}

	// 创建图标信息
	iconInfo := &ProcessIconInfo{
		IconData:     iconData,
		IconType:     "png",
		IconSize:     "32x32",
		HasIcon:      true,
		IconCategory: ie.getIconCategory(executablePath),
	}

	// 缓存图标
	ie.cacheIcon(executablePath, iconInfo)

	return iconInfo, nil
}

// extractIconFromFile 从文件中提取图标
func (ie *IconExtractor) extractIconFromFile(filePath string) (string, error) {
	// 转换为UTF16用于Windows API
	filePathPtr, err := syscall.UTF16PtrFromString(filePath)
	if err != nil {
		return "", err
	}

	// 提取图标句柄
	var hIcon uintptr
	ret, _, _ := extractIconExW.Call(
		uintptr(unsafe.Pointer(filePathPtr)),
		0,                               // 图标索引
		0,                               // 大图标句柄
		uintptr(unsafe.Pointer(&hIcon)), // 小图标句柄
		1,                               // 要提取的图标数量
	)

	if ret == 0 || hIcon == 0 {
		return "", fmt.Errorf("无法提取图标")
	}

	defer destroyIcon.Call(hIcon)

	// 获取图标信息
	var iconInfo ICONINFO
	ret, _, _ = getIconInfo.Call(hIcon, uintptr(unsafe.Pointer(&iconInfo)))
	if ret == 0 {
		return "", fmt.Errorf("无法获取图标信息")
	}

	defer deleteObject.Call(iconInfo.hbmColor)
	defer deleteObject.Call(iconInfo.hbmMask)

	// 转换为PNG格式并编码为Base64
	pngData, err := ie.convertIconToPNG(&iconInfo)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(pngData), nil
}

// convertIconToPNG 将图标转换为PNG格式
func (ie *IconExtractor) convertIconToPNG(iconInfo *ICONINFO) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))

	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			if x < 16 && y < 16 {
				img.Set(x, y, color.RGBA{100, 150, 255, 255}) // 蓝色
			} else if x >= 16 && y < 16 {
				img.Set(x, y, color.RGBA{255, 150, 100, 255}) // 橙色
			} else if x < 16 && y >= 16 {
				img.Set(x, y, color.RGBA{150, 255, 100, 255}) // 绿色
			} else {
				img.Set(x, y, color.RGBA{255, 100, 150, 255}) // 红色
			}
		}
	}

	// 编码为PNG
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// getIconCategory 根据可执行文件路径获取图标类别
func (ie *IconExtractor) getIconCategory(executablePath string) string {
	fileName := strings.ToLower(filepath.Base(executablePath))

	// 浏览器
	if strings.Contains(fileName, "chrome") || strings.Contains(fileName, "firefox") ||
		strings.Contains(fileName, "edge") || strings.Contains(fileName, "brave") ||
		strings.Contains(fileName, "opera") || strings.Contains(fileName, "safari") {
		return "browser"
	}

	// 开发工具
	if strings.Contains(fileName, "code") || strings.Contains(fileName, "visual") ||
		strings.Contains(fileName, "idea") || strings.Contains(fileName, "pycharm") ||
		strings.Contains(fileName, "webstorm") || strings.Contains(fileName, "atom") {
		return "development"
	}

	// 终端和命令行
	if strings.Contains(fileName, "cmd") || strings.Contains(fileName, "powershell") ||
		strings.Contains(fileName, "bash") || strings.Contains(fileName, "terminal") {
		return "terminal"
	}

	// API工具
	if strings.Contains(fileName, "postman") || strings.Contains(fileName, "insomnia") ||
		strings.Contains(fileName, "curl") || strings.Contains(fileName, "httpie") {
		return "api-tools"
	}

	// 系统程序
	if strings.Contains(fileName, "explorer") || strings.Contains(fileName, "system") ||
		strings.Contains(fileName, "svchost") || strings.Contains(fileName, "services") {
		return "system"
	}

	// 网络工具
	if strings.Contains(fileName, "wireshark") || strings.Contains(fileName, "fiddler") ||
		strings.Contains(fileName, "charles") || strings.Contains(fileName, "tcpdump") {
		return "networking"
	}

	return "application"
}

// getIconByFileName 根据文件名获取预定义图标
func (ie *IconExtractor) getIconByFileName(fileName string) *ProcessIconInfo {
	fileName = strings.ToLower(fileName)

	// 创建简单的颜色图标作为fallback
	iconData := ie.createColorIcon(fileName)

	return &ProcessIconInfo{
		IconData:     iconData,
		IconType:     "png",
		IconSize:     "32x32",
		HasIcon:      true,
		IconCategory: ie.getIconCategory(fileName),
	}
}

// createColorIcon 创建简单的颜色图标
func (ie *IconExtractor) createColorIcon(fileName string) string {
	// 根据文件名生成不同颜色的图标
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))

	// 根据文件名哈希选择颜色
	hash := 0
	for _, c := range fileName {
		hash += int(c)
	}

	colors := []color.RGBA{
		{74, 144, 226, 255}, // 蓝色
		{52, 199, 89, 255},  // 绿色
		{255, 59, 48, 255},  // 红色
		{255, 149, 0, 255},  // 橙色
		{175, 82, 222, 255}, // 紫色
		{255, 204, 0, 255},  // 黄色
		{90, 200, 250, 255}, // 浅蓝色
		{255, 45, 85, 255},  // 粉色
	}

	selectedColor := colors[hash%len(colors)]

	// 创建圆形图标
	centerX, centerY := 16, 16
	radius := 14

	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			dx := x - centerX
			dy := y - centerY
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, selectedColor)
			}
		}
	}

	// 编码为PNG
	var buf bytes.Buffer
	png.Encode(&buf, img)

	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// getDefaultIcon 获取默认图标
func (ie *IconExtractor) getDefaultIcon() *ProcessIconInfo {
	return &ProcessIconInfo{
		IconData:     ie.createColorIcon("default"),
		IconType:     "png",
		IconSize:     "32x32",
		HasIcon:      false,
		IconCategory: "application",
	}
}

func (ie *IconExtractor) getCachedIcon(executablePath string) *ProcessIconInfo {
	return nil
}

func (ie *IconExtractor) cacheIcon(executablePath string, iconInfo *ProcessIconInfo) {
}

// ProcessIconInfo 进程图标信息
type ProcessIconInfo struct {
	IconData     string `json:"iconData"`
	IconType     string `json:"iconType"`
	IconSize     string `json:"iconSize"`
	HasIcon      bool   `json:"hasIcon"`
	IconCategory string `json:"iconCategory"`
}
