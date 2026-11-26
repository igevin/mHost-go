package desktop

import (
	"testing"

	"github.com/igevin/mHost-go/internal/app/demo"
)

func TestApp_CleanupIsIdempotent(t *testing.T) {
	model := NewWorkbenchModel(demo.NewStorageService(), &stubDirectoryPicker{})
	app := NewApp(model)
	if err := app.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := app.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if !model.closed {
		t.Fatal("cleanup did not close the workbench")
	}
}

func TestApp_CleanupAllowsNilModel(t *testing.T) {
	app := NewApp(nil)
	if err := app.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestApp_CloseHidesOnlyWhenNotQuitting(t *testing.T) {
	app := &App{}
	if !app.shouldHideOnClose() {
		t.Fatal("ordinary close should hide the window")
	}
	app.quitting = true
	if app.shouldHideOnClose() {
		t.Fatal("quit should allow the window to close")
	}
}
