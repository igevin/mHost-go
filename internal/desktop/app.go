package desktop

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type closeServer interface {
	Close() error
}

type App struct {
	Model    *Model
	manager  DNSManager
	server   closeServer
	window   *mygo.Window
	tray     *mygo.Tray
	quitting bool

	cleanupOnce sync.Once
	cleanupDone atomic.Bool
	cleanupErr  error
}

func NewApp(model *Model, manager DNSManager, server closeServer) *App {
	return &App{Model: model, manager: manager, server: server}
}

func (a *App) Run() error {
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleWindowMenu},
	}))
	mygo.App.OnBeforeQuit(func(event *mygo.QuitEvent) {
		a.quitting = true
		if a.cleanupDone.Load() {
			return
		}
		if event.SessionEnding {
			_ = a.Cleanup()
			return
		}
		event.PreventDefault()
		go func() {
			_ = a.Cleanup()
			mygo.App.Quit()
		}()
	})
	mygo.App.OnQuitCanceled(func() {
		a.quitting = false
	})
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
	defer a.Cleanup()
	return mygo.App.Run()
}

func (a *App) Cleanup() error {
	a.cleanupOnce.Do(func() {
		defer a.cleanupDone.Store(true)
		var stopErr error
		if a.manager != nil {
			stopErr = a.manager.Stop(context.Background())
		}
		var serverErr error
		if a.server != nil {
			serverErr = a.server.Close()
		}
		a.cleanupErr = errors.Join(stopErr, serverErr)
	})
	return a.cleanupErr
}

func (a *App) shouldHideOnClose() bool {
	return !a.quitting
}

func (a *App) ready() {
	a.window = mygo.NewWindow(mygo.WindowOptions{
		Title:   "mHost Phase 0",
		Width:   1100,
		Height:  720,
		Content: ui.View(a.Model.View),
	})
	a.window.OnClose(func(event *mygo.CloseEvent) {
		if a.shouldHideOnClose() {
			event.PreventDefault()
			a.window.Hide()
		}
	})
	a.Model.OnDNSStart = func() {
		a.Model.DNSStatus = "Starting DNS test…"
		go func() {
			err := a.manager.Start(context.Background())
			a.window.Update(func() {
				if err != nil {
					a.Model.DNSStatus = "Start failed; recovery attempted: " + err.Error()
				} else {
					a.Model.DNSStatus = "DNS test is running"
				}
			})
		}()
	}
	a.Model.OnDNSStop = func() {
		a.Model.DNSStatus = "Stopping DNS test…"
		go func() {
			err := a.manager.Stop(context.Background())
			a.window.Update(func() {
				if err != nil {
					a.Model.DNSStatus = "Stop needs recovery on next launch: " + err.Error()
				} else {
					a.Model.DNSStatus = "DNS test is stopped and original DNS is verified"
				}
			})
		}()
	}
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        "mHost Phase 0",
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Open mHost", Click: func(*mygo.MenuItem, *mygo.Window) { a.showWindow() }},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}),
	})
	if err != nil {
		a.Model.DNSStatus = "Tray unavailable: " + err.Error()
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
