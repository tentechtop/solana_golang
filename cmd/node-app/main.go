package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/tentechtop/ordexus-core/internal/nodeapp"
)

func main() {
	defaultRoot, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		defaultRoot = local
	}
	directory := flag.String("data-dir", filepath.Join(defaultRoot, "OrdexusNode"), "independent application data directory")
	noOpen := flag.Bool("no-open", false, "do not open the management panel automatically")
	flag.Parse()
	controller, err := nodeapp.New(*directory, "windows")
	if err != nil {
		if data, readErr := os.ReadFile(filepath.Join(*directory, "panel-session.json")); readErr == nil {
			var session struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(data, &session)
			parts := strings.Split(session.URL, "#")
			if len(parts) == 2 && strings.HasPrefix(parts[0], "http://127.0.0.1:") {
				request, _ := http.NewRequest("GET", parts[0]+"api/status", nil)
				request.Header.Set("Authorization", "Bearer "+parts[1])
				response, checkErr := (&http.Client{Timeout: time.Second}).Do(request)
				if checkErr == nil {
					response.Body.Close()
					if response.StatusCode == 200 {
						if !*noOpen {
							openURL(session.URL)
						}
						return
					}
				}
			}
		}
		showError(err.Error())
		return
	}
	defer controller.Close()
	panel, err := controller.OpenPanel()
	if err != nil {
		showError(err.Error())
		return
	}
	data, _ := json.Marshal(map[string]string{"url": panel})
	if err = os.WriteFile(filepath.Join(*directory, "panel-session.json"), data, 0600); err != nil {
		showError(err.Error())
		return
	}
	if !*noOpen {
		openURL(panel)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case <-controller.Done():
	case <-ctx.Done():
	}
}
