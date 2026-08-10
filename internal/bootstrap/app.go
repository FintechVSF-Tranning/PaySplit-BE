package bootstrap

import "context"

type App struct{}

func New(ctx context.Context) (*App, error) {
	panic("TODO: implement bootstrap.New")
}

func (a *App) Address() string {
	panic("TODO: implement App.Address")
}

func (a *App) Start() error {
	panic("TODO: implement App.Start")
}

func (a *App) Shutdown(ctx context.Context) error {
	panic("TODO: implement App.Shutdown")
}
