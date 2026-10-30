// Command gh-agent（桌面壳）常驻托盘，轮询 GitHub 上的触发词，
// 用本地模型出稿，进待审列表由人决定发不发。
//
//	go run .            # 开发
//	npm run dev         # 带 Vite 热更新
//	npm run build       # 打包 Windows 安装包
package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"log"
	"sync"

	"github.com/ayuayue/gh-agent/internal/desk"
	"github.com/egoist/mygo"
)

//go:embed resources/icon.png
var appIcon []byte

var (
	winMu sync.Mutex
	win   *mygo.Window
)

func main() {
	app := mygo.App
	// 名字决定用户数据目录，要在任何存储之前设好。
	app.SetName("GH Agent")

	svc, err := desk.New("")
	if err != nil {
		log.Fatal(err)
	}
	mygo.Bind(svc)

	app.WhenReady(func() {
		app.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))

		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID:      "main",
			Icon:    trayIcon(32),
			ToolTip: "GH Agent",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "显示窗口", Click: func(*mygo.MenuItem, *mygo.Window) { showWindow() }},
				mygo.Separator(),
				{Label: "开始轮询", Click: func(*mygo.MenuItem, *mygo.Window) { _, _ = svc.Start() }},
				{Label: "停止轮询", Click: func(*mygo.MenuItem, *mygo.Window) { svc.Stop() }},
				mygo.Separator(),
				{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { app.Quit() }},
			}),
		})
		if err != nil {
			log.Printf("托盘创建失败（窗口仍可用）：%v", err)
		} else {
			tray.OnClick(showWindow)
		}

		showWindow()
		// 常驻后台：关掉窗口不退出应用。
		if _, err := svc.Start(); err != nil {
			log.Printf("自动开始轮询失败：%v", err)
		}
	})

	// 关掉所有窗口后留在托盘里，点托盘图标再开。
	app.OnWindowAllClosed(func() {})
	app.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			showWindow()
		}
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func showWindow() {
	winMu.Lock()
	defer winMu.Unlock()
	if win != nil && !win.IsDestroyed() {
		win.Show()
		win.Focus()
		return
	}
	win = mygo.NewWindow(mygo.WindowOptions{
		Title:           "GH Agent",
		Width:           1040,
		Height:          740,
		MinWidth:        760,
		MinHeight:       520,
		BackgroundColor: "light-dark(#f6f7f9, #0f1115)", // 页面 --bg
		StateKey:        "main",                          // 记住上次的位置和大小
		URL:             "/",                             // dev 走 devUrl，打包后走 frontendDist
	})
}

// trayIcon 把应用图标缩到托盘要的尺寸（Windows 16pt 起步，给 32px 让系统缩）。
func trayIcon(size int) []byte {
	src, err := png.Decode(bytes.NewReader(appIcon))
	if err != nil {
		return appIcon
	}
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := bounds.Min.X + x*bounds.Dx()/size
			sy := bounds.Min.Y + y*bounds.Dy()/size
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return appIcon
	}
	return buf.Bytes()
}
