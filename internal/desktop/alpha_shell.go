package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	app "github.com/igevin/mHost-go/internal/app"
	"github.com/igevin/mHost-go/internal/app/alphaboot"
)

// Alpha 导航页：Hosts 为主链路；其余页面 #26 实现，本阶段标记未开放。
var alphaNavTabs = []string{"Hosts", "Snapshots", "System Hosts", "Settings"}

// AlphaModel 是 Hosts-only Alpha 的应用壳（#25）：导航 + Hosts 主链路
// + 启动信息（迁移回执/issue）展示。生命周期语义委托给内部
// HostsWorkbenchModel（退出门禁、写不可取消等沿用 #13 安全语义）。
type AlphaModel struct {
	Hosts *HostsWorkbenchModel

	SelectedNav int
	Boot        *alphaboot.Result
	bannerShown bool
	started     bool
}

func NewAlphaModel(boot *alphaboot.Result) *AlphaModel {
	hosts := NewHostsWorkbenchModel(boot.Service)
	hosts.Admin = boot.Service
	hosts.Chromeless = true
	return &AlphaModel{
		Hosts: hosts,
		Boot:  boot,
	}
}

func (m *AlphaModel) Title() string { return "mHost Alpha" }

// Start 在窗口就绪后自动加载 profile（Alpha 是产品形态，无手动 Load 按钮）。
func (m *AlphaModel) Start() {
	if m.started {
		return
	}
	m.started = true
	m.Hosts.Load()
}

func (m *AlphaModel) View(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Gap(12).Padding(10, 20).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			ui.Text(c, "mHost").FontSize(20).Bold()
			ui.Badge(c, "Alpha · Hosts only")
			ui.Spacer(c)
			if m.Hosts.busy() {
				ui.Text(c, "working…").FontSize(12).TextColor(theme.TextMuted)
			}
		})
		m.bootBanner(c)
		ui.Row(c).Padding(8, 20, 0, 20).Children(func() {
			ui.Tabs(c, &m.SelectedNav, alphaNavTabs...)
		})
		switch m.SelectedNav {
		case 0:
			m.Hosts.View(c)
		default:
			m.comingSoonView(c, alphaNavTabs[m.SelectedNav])
		}
	})
}

// bootBanner 一次性展示启动迁移回执与扫描 issue；可关闭。
func (m *AlphaModel) bootBanner(c *ui.Context) {
	if m.Boot == nil || m.bannerShown {
		return
	}
	theme := c.Theme()
	migrationDone := m.Boot.Migration != nil
	if !migrationDone && len(m.Boot.Issues) == 0 {
		return
	}
	ui.Row(c).Gap(10).Padding(8, 20).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			if migrationDone {
				ui.Text(c, "Data upgraded from v1 to v2 on this launch.").Bold()
				ui.Text(c, receiptSummaryLine(*m.Boot.Migration)).FontSize(11).TextColor(theme.TextMuted)
			}
			for _, issue := range m.Boot.Issues {
				ui.Text(c, fmt.Sprintf("%s: %s", issue.Code, issue.Message)).FontSize(11).TextColor(theme.TextMuted)
			}
		})
		if ui.Button(c, "Dismiss").Clicked() {
			m.bannerShown = true
		}
	})
}

func receiptSummaryLine(receipt app.WriteReceipt) string {
	written, skipped, failed := 0, 0, 0
	for _, result := range receipt.Results {
		switch result.Status {
		case app.WriteWritten, app.WriteUnchanged:
			written++
		case app.WriteSkipped:
			skipped++
		case app.WriteFailed:
			failed++
		}
	}
	return fmt.Sprintf("Migration receipt · %d written · %d skipped · %d failed", written, skipped, failed)
}

// comingSoonView 标记未开放页面（#26 实现；DNS/AdBlock 不在 Alpha 范围）。
func (m *AlphaModel) comingSoonView(c *ui.Context, name string) {
	theme := c.Theme()
	ui.Column(c).Fill().Center().Gap(8).Padding(32).Children(func() {
		ui.Text(c, name).FontSize(22).Bold()
		ui.Text(c, "This page ships in a later Alpha slice.").TextColor(theme.TextMuted)
		ui.Text(c, "DNS and AdBlock stay hidden in the Hosts-only Alpha.").FontSize(12).TextColor(theme.TextMuted)
	})
}

// 生命周期与 App 接线：全部委托给 Hosts 主链路。
func (m *AlphaModel) RequestQuit() bool { return m.Hosts.RequestQuit() }
func (m *AlphaModel) CancelQuit()       { m.Hosts.CancelQuit() }
func (m *AlphaModel) Close() error      { return m.Hosts.Close() }
func (m *AlphaModel) SetDispatch(fn func(func())) {
	m.Hosts.SetDispatch(fn)
}
func (m *AlphaModel) SetOnSafeToQuit(fn func()) { m.Hosts.SetOnSafeToQuit(fn) }
func (m *AlphaModel) SetProblem(p *app.UIError) { m.Hosts.SetProblem(p) }
