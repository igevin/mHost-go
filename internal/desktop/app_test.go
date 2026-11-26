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

func TestApp_QuitCanceledPreservesDeferredWriteQuit(t *testing.T) {
	model := NewWorkbenchModel(demo.NewStorageService(), &stubDirectoryPicker{})
	model.Phase = PhaseWriting
	app := NewApp(model)
	if model.RequestQuit() {
		t.Fatal("writing did not defer quit")
	}
	app.deferredQuit = true
	app.quitting = true
	app.quitCanceled()

	if !model.QuitRequested {
		t.Fatal("framework quit cancellation cleared the deferred write quit")
	}
	if app.deferredQuit || !app.shouldHideOnClose() {
		t.Fatal("deferred quit left window close in the quitting state")
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
