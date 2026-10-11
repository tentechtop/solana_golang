// Package nodemobile is the narrow gomobile bridge for the standalone node app.
package nodemobile

import "github.com/tentechtop/ordexus-core/internal/nodeapp"

type App struct{ controller *nodeapp.Controller }

func NewApp(directory string) (*App, error) {
	controller, err := nodeapp.New(directory, "android")
	if err != nil {
		return nil, err
	}
	return &App{controller: controller}, nil
}

func (app *App) PanelURL() (string, error) { return app.controller.OpenPanel() }
func (app *App) Start() error              { return app.controller.Start() }
func (app *App) Stop() error               { return app.controller.Stop() }
func (app *App) Running() bool             { return app.controller.Running() }
func (app *App) Close() error              { return app.controller.Close() }
