package desktop

import (
	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
)

// StartupErrorModel 在启动准备失败时给用户一个可见的错误窗口，
// 而不是只写 stderr 后退出（GUI 从 Finder 启动时 stderr 不可见；
// review 建议项）。仅提供 Quit。
type StartupErrorModel struct {
	Err      error
	onQuit   func()
	dispatch func(func())
}

func NewStartupErrorModel(err error) *StartupErrorModel {
	return &StartupErrorModel{Err: err}
}

func (m *StartupErrorModel) Title() string { return "mHost · Startup Error" }

func (m *StartupErrorModel) View(c *ui.Context) {
	theme := c.Theme()
	message := "Unknown startup error"
	detail := ""
	if m.Err != nil {
		message = m.Err.Error()
		if uiErr, ok := m.Err.(*app.UIError); ok {
			if uiErr.Detail != "" {
				detail = uiErr.Detail
			}
		}
	}
	ui.Column(c).Fill().Center().Gap(12).Padding(32).Children(func() {
		ui.Text(c, "mHost could not start").FontSize(22).Bold()
		ui.Text(c, message).TextColor(theme.TextMuted)
		if detail != "" {
			ui.Text(c, detail).FontSize(12).TextColor(theme.TextMuted)
		}
		if ui.PrimaryButton(c, "Quit").Clicked() && m.onQuit != nil {
			m.onQuit()
		}
	})
}

func (m *StartupErrorModel) RequestQuit() bool { return true }
func (m *StartupErrorModel) CancelQuit()       {}
func (m *StartupErrorModel) Close() error      { return nil }
func (m *StartupErrorModel) SetDispatch(fn func(func())) {
	m.dispatch = fn
}
func (m *StartupErrorModel) SetOnSafeToQuit(fn func()) { m.onQuit = fn }
func (m *StartupErrorModel) SetProblem(*app.UIError)   {}
