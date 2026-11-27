package desktop

import (
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
)

// AppModel is what App requires of a workbench model. Both the storage
// workbench and the hosts rewrite workbench satisfy it.
type AppModel interface {
	View(c *ui.Context)
	RequestQuit() bool
	CancelQuit()
	Close() error
	SetDispatch(func(func()))
	SetOnSafeToQuit(func())
	SetProblem(*app.UIError)
}

// TitledModel 可选提供窗口标题；App 用它设置窗口与托盘文案。
type TitledModel interface {
	Title() string
}

type App struct {
	Model  AppModel
	window *mygo.Window
	tray   *mygo.Tray

	quitting       bool
	deferredQuit   bool
	cleanupOnce    sync.Once
	cleanupStarted atomic.Bool
	cleanupDone    atomic.Bool
	cleanupErr     error
}

func NewApp(model AppModel) *App {
	return &App{Model: model}
}

func (a *App) Run() error {
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleWindowMenu},
	}))
	mygo.App.OnBeforeQuit(func(event *mygo.QuitEvent) {
		if a.cleanupDone.Load() {
			a.quitting = true
			return
		}
		if a.Model != nil && !a.Model.RequestQuit() {
			a.deferredQuit = true
			event.PreventDefault()
			return
		}
		a.quitting = true
		if event.SessionEnding {
			_ = a.Cleanup()
			return
		}
		event.PreventDefault()
		a.finishQuit()
	})
	mygo.App.OnQuitCanceled(a.quitCanceled)
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if !a.cleanupDone.Load() {
			_ = a.Cleanup()
		}
		if a.tray != nil {
			a.tray.Destroy()
		}
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			a.showWindow()
		}
	})
	mygo.App.WhenReady(a.ready)
	defer func() {
		_ = a.Cleanup()
	}()
	return mygo.App.Run()
}

func (a *App) Cleanup() error {
	a.cleanupOnce.Do(func() {
		defer a.cleanupDone.Store(true)
		if a.Model != nil {
			a.cleanupErr = a.Model.Close()
		}
	})
	return a.cleanupErr
}

func (a *App) finishQuit() {
	if a.cleanupStarted.Swap(true) {
		return
	}
	a.quitting = true
	_ = a.Cleanup()
	go mygo.App.Quit()
}

func (a *App) quitCanceled() {
	a.quitting = false
	if a.deferredQuit {
		a.deferredQuit = false
		return
	}
	if a.Model != nil {
		a.Model.CancelQuit()
	}
}

func (a *App) shouldHideOnClose() bool {
	return !a.quitting
}

func (a *App) ready() {
	title := "mHost"
	if titled, ok := a.Model.(TitledModel); ok {
		title = titled.Title()
	}
	a.window = mygo.NewWindow(mygo.WindowOptions{
		Title:   title,
		Width:   1120,
		Height:  760,
		Content: ui.View(a.Model.View),
	})
	a.window.OnClose(func(event *mygo.CloseEvent) {
		if a.shouldHideOnClose() {
			event.PreventDefault()
			a.window.Hide()
		}
	})
	a.Model.SetDispatch(a.window.Update)
	a.Model.SetOnSafeToQuit(a.finishQuit)
	// Starter 可选接口：窗口就绪后自动加载（Alpha 产品形态无手动 Load）。
	if starter, ok := a.Model.(interface{ Start() }); ok {
		starter.Start()
	}
	if pickerModel, ok := a.Model.(interface{ DirectoryPicker() app.DirectoryPicker }); ok {
		if picker, ok := pickerModel.DirectoryPicker().(interface{ SetParent(*mygo.Window) }); ok {
			picker.SetParent(a.window)
		}
	}
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        title,
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Open mHost", Click: func(*mygo.MenuItem, *mygo.Window) { a.showWindow() }},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}),
	})
	if err != nil {
		a.Model.SetProblem(classifyUIError("create_tray", err))
		return
	}
	a.tray = tray
}

func (a *App) showWindow() {
	if a.window != nil {
		a.window.Show()
		a.window.Focus()
	}
}
