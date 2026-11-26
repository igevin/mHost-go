package desktop

import (
	"context"

	"github.com/egoist/mygo"
	app "github.com/igevin/mHost-go/internal/app"
)

type NativeDirectoryPicker struct {
	parent *mygo.Window
}

func (p *NativeDirectoryPicker) SetParent(parent *mygo.Window) {
	p.parent = parent
}

func (p *NativeDirectoryPicker) PickDirectory(ctx context.Context, opts app.DirectoryOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: p.parent, Title: opts.Title, DefaultPath: opts.DefaultPath,
		ButtonLabel: opts.ButtonLabel, Message: opts.Message,
		Directory: true, CreateDirectories: opts.CreateDirectories,
	})
	if err != nil {
		return "", &app.UIError{Kind: app.ErrorPickerFailed, Message: "Directory picker failed", Detail: err.Error(), Retryable: true, Cause: err}
	}
	if len(paths) == 0 || paths[0] == "" {
		return "", app.ErrPickerCancelled
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return paths[0], nil
}
