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
	"sync"
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
	deleteObject   = gdi32.NewProc("DeleteObject")
	destroyIcon    = user32.NewProc("DestroyIcon")
	getObjectW     = gdi32.NewProc("GetObjectW")
	getDIBits      = gdi32.NewProc("GetDIBits")
	getDC          = user32.NewProc("GetDC")
	releaseDC      = user32.NewProc("ReleaseDC")
)

// bitmapInfoHeader 对应 Win32 BITMAPINFOHEADER。
type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

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
	mu    sync.Mutex
	cache map[string]*ProcessIconInfo // 按可执行路径的内存缓存
}

// NewIconExtractor 创建图标提取器
func NewIconExtractor() *IconExtractor {
	return &IconExtractor{
		cache: make(map[string]*ProcessIconInfo),
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
	iconData, size, err := ie.extractIconFromFile(executablePath)
	if err != nil {
		// 如果提取失败，返回基于文件名的默认图标
		return ie.getIconByFileName(filepath.Base(executablePath)), nil
	}

	sizeStr := "32x32"
	if size > 0 {
		sizeStr = fmt.Sprintf("%dx%d", size, size)
	}

	// 创建图标信息
	iconInfo := &ProcessIconInfo{
		IconData:     iconData,
		IconType:     "png",
		IconSize:     sizeStr,
		HasIcon:      true,
		IconCategory: ie.getIconCategory(executablePath),
	}

	// 缓存图标
	ie.cacheIcon(executablePath, iconInfo)

	return iconInfo, nil
}

// extractIconFromFile 从文件中提取真实图标,返回 base64(PNG)与像素边长。
func (ie *IconExtractor) extractIconFromFile(filePath string) (string, int, error) {
	// 转换为UTF16用于Windows API
	filePathPtr, err := syscall.UTF16PtrFromString(filePath)
	if err != nil {
		return "", 0, err
	}

	// 提取图标句柄(取大图标,质量更好)
	var hIcon uintptr
	ret, _, _ := extractIconExW.Call(
		uintptr(unsafe.Pointer(filePathPtr)),
		0,                               // 图标索引
		uintptr(unsafe.Pointer(&hIcon)), // 大图标句柄
		0,                               // 小图标句柄
		1,                               // 要提取的图标数量
	)

	if ret == 0 || hIcon == 0 {
		return "", 0, fmt.Errorf("无法提取图标")
	}

	defer destroyIcon.Call(hIcon)

	// 获取图标信息
	var iconInfo ICONINFO
	ret, _, _ = getIconInfo.Call(hIcon, uintptr(unsafe.Pointer(&iconInfo)))
	if ret == 0 {
		return "", 0, fmt.Errorf("无法获取图标信息")
	}

	if iconInfo.hbmColor != 0 {
		defer deleteObject.Call(iconInfo.hbmColor)
	}
	if iconInfo.hbmMask != 0 {
		defer deleteObject.Call(iconInfo.hbmMask)
	}

	// 读取真实像素并转换为PNG格式,再编码为Base64
	pngData, size, err := ie.convertIconToPNG(&iconInfo)
	if err != nil {
		return "", 0, err
	}

	return base64.StdEncoding.EncodeToString(pngData), size, nil
}

// convertIconToPNG 读取图标颜色位图的真实像素并编码为 PNG,返回(png 字节, 边长)。
//
// 流程:GetObject 取尺寸 → GetDIBits 以 32 位自上而下 DIB 取出 BGRA 像素 → 转 RGBA → PNG。
// 全程进程内 GDI 调用(无子进程),失败返回 error 由上层回退到色块图标。
func (ie *IconExtractor) convertIconToPNG(iconInfo *ICONINFO) ([]byte, int, error) {
	if iconInfo.hbmColor == 0 {
		return nil, 0, fmt.Errorf("图标无颜色位图")
	}

	// 取颜色位图的真实宽高。
	var bm BITMAP
	if r, _, _ := getObjectW.Call(iconInfo.hbmColor, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil, 0, fmt.Errorf("GetObject 失败")
	}
	w, h := int(bm.bmWidth), int(bm.bmHeight)
	if w <= 0 || h <= 0 || w > 512 || h > 512 {
		return nil, 0, fmt.Errorf("图标尺寸无效: %dx%d", w, h)
	}

	hdc, _, _ := getDC.Call(0)
	if hdc == 0 {
		return nil, 0, fmt.Errorf("GetDC 失败")
	}
	defer releaseDC.Call(0, hdc)

	bi := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       int32(w),
		biHeight:      int32(-h), // 负高度 = 自上而下,行序与图像一致
		biPlanes:      1,
		biBitCount:    32,
		biCompression: 0, // BI_RGB
	}
	buf := make([]byte, w*h*4)
	r, _, _ := getDIBits.Call(hdc, iconInfo.hbmColor, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0)
	if r == 0 {
		return nil, 0, fmt.Errorf("GetDIBits 失败")
	}

	pngData, err := bgraToPNG(buf, w, h)
	if err != nil {
		return nil, 0, err
	}
	return pngData, w, nil
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
	ie.mu.Lock()
	defer ie.mu.Unlock()
	return ie.cache[executablePath]
}

func (ie *IconExtractor) cacheIcon(executablePath string, iconInfo *ProcessIconInfo) {
	ie.mu.Lock()
	defer ie.mu.Unlock()
	ie.cache[executablePath] = iconInfo
}

// ProcessIconInfo 进程图标信息
type ProcessIconInfo struct {
	IconData     string `json:"iconData"`
	IconType     string `json:"iconType"`
	IconSize     string `json:"iconSize"`
	HasIcon      bool   `json:"hasIcon"`
	IconCategory string `json:"iconCategory"`
}
