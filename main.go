package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
)

type Config struct {
	Sniper   string `json:"sniper"`
	GuildID  string `json:"guild_id"`
	Password string `json:"password"`
}

type MFAPayload struct {
	Ticket string `json:"ticket"`
	Type   string `json:"mfa_type"`
	Data   string `json:"data"`
}

type MFAResponse struct {
	Token string `json:"token"`
}

type VanityResponse struct {
	MFA struct {
		Ticket string `json:"ticket"`
	} `json:"mfa"`
}

var (
	fastHttpClient = &fasthttp.Client{
		TLSConfig:       &tls.Config{InsecureSkipVerify: true},
		MaxConnsPerHost: 1000,
	}
	currentMFAToken string
	mfaMutex        sync.RWMutex
	config          Config
)

func loadConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	return nil
}

func setCommonHeaders(req *fasthttp.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x32) "+
		"AppleWebKit/537.36 (KHTML, like Gecko) discord/1.0.9164 "+
		"Chrome/124.0.6367.243 Electron/30.2.0 Safari/537.36")
	req.Header.Set("X-Super-Properties", "eyJvcyI6IldpbmRvd3MiLCJicm93c2VyIjoiRGlzY29yZCBDbGllbnQiLCJyZWxlYXNlX2NoYW5uZWwiOiJzdGFibGUiLCJjbGllbnRfdmVyc2lvbiI6IjEuMC45MTY0Iiwib3NfdmVyc2lvbiI6IjEwLjAuMjI2MzEiLCJvc19hcmNoIjoieDY0IiwiYXBwX2FyY2giOiJ4NjQiLCJzeXN0ZW1fbG9jYWxlIjoidHIiLCJicm93c2VyX3VzZXJfYWdlbnQiOiJNb3ppbGxhLzUuMCAoV2luZG93cyBOVCAxMC4wOyBXaW42NDsgeDY0KSBBcHBsZVdlYktpdC81MzcuMzYgKEtIVE1MLCBsaWtlIEdlY2tvKSBkaXNjb3JkLzEuMC45MTY0IENocm9tZS8xMjQuMC42MzY3LjI0MyBFbGVjdHJvbi8zMC4yLjAgU2FmYXJpLzUzNy4zNiIsImJyb3dzZXJfdmVyc2lvbiI6IjMwLjIuMCIsIm9zX3Nka192ZXJzaW9uIjoiMjI2MzEiLCJjbGllbnRfdnVibF9udW1iZXIiOjUyODI2LCJjbGllbnRfZXZlbnRfc291cmNlIjpudWxsfQ==")
	req.Header.Set("X-Discord-Timezone", "Europe/France")
	req.Header.Set("X-Discord-Locale", "en-US")
	req.Header.Set("X-Debug-Options", "bugReporterEnabled")
	req.Header.Set("Content-Type", "application/json")
}

func updateMFATokenFile() {
	for {
		mfaMutex.RLock()
		token := currentMFAToken
		mfaMutex.RUnlock()

		if token != "" {
			err := os.WriteFile("mfa_token.txt", []byte(token), 0644)
			if err != nil {
				logrus.Errorf("Failed to write MFA token to file: %v", err)
			}
		}

		time.Sleep(5 * time.Minute)
	}
}

func GetMFATicket(token, guildID, vanityURL string) (string, error) {
	body := []byte("{\"code\":\"" + vanityURL + "\"}")

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	url := "https://discord.com/api/v9/guilds/" + guildID + "/vanity-url"
	req.SetRequestURI(url)
	req.Header.SetMethod("PATCH")
	setCommonHeaders(req, token)
	req.SetBody(body)

	err := fastHttpClient.Do(req, resp)
	if err != nil {
		return "", fmt.Errorf("failed to get MFA ticket: %v", err)
	}

	if resp.StatusCode() != fasthttp.StatusUnauthorized {
		bodyBytes := resp.Body()
		return "", fmt.Errorf("failed to get MFA ticket: Expected unauthorized status, got: %d - %s", resp.StatusCode(), string(bodyBytes))
	}

	bodyBytes := resp.Body()
	var vanityResponse VanityResponse
	if err := json.Unmarshal(bodyBytes, &vanityResponse); err != nil {
		return "", fmt.Errorf("failed to process response: %s", err)
	}

	return vanityResponse.MFA.Ticket, nil
}

func SendMFA(token, ticket, password string) (string, error) {
	payload := MFAPayload{
		Ticket: ticket,
		Type:   "password",
		Data:   password,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("error marshalling to JSON: %s", err)
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("https://discord.com/api/v9/mfa/finish")
	req.Header.SetMethod("POST")
	setCommonHeaders(req, token)
	req.SetBody(jsonPayload)

	err = fastHttpClient.Do(req, resp)
	if err != nil {
		return "", fmt.Errorf("network error: %s", err)
	}

	bodyBytes := resp.Body()

	if resp.StatusCode() == fasthttp.StatusOK {
		var mfaResponse MFAResponse
		err := json.Unmarshal(bodyBytes, &mfaResponse)
		if err != nil {
			return "", fmt.Errorf("JSON Error: %s - %s - %d", err, string(bodyBytes), resp.StatusCode())
		}
		return mfaResponse.Token, nil
	}

	return "", fmt.Errorf("error: %s - %d", string(bodyBytes), resp.StatusCode())
}

func HandleMFA(token, guildID, vanityURL, password string) (string, error) {
	ticket, err := GetMFATicket(token, guildID, vanityURL)
	if err != nil {
		return "", fmt.Errorf("failed to get MFA ticket: %v", err)
	}

	mfaToken, err := SendMFA(token, ticket, password)
	if err != nil {
		return "", fmt.Errorf("failed to send MFA: %v", err)
	}

	mfaMutex.Lock()
	currentMFAToken = mfaToken
	mfaMutex.Unlock()

	return mfaToken, nil
}

func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logrus.SetLevel(logrus.InfoLevel)

	if err := loadConfig("config.json"); err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}

	logrus.Info("Starting MFA token monitor...")
	logrus.Infof("Using sniper token for guild ID: %s", config.GuildID)

	// Start the MFA token update goroutine
	go func() {
		for {
			ticket, err := GetMFATicket(config.Sniper, config.GuildID, "test")
			if err != nil {
				logrus.Errorf("Failed to get MFA ticket: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			mfaToken, err := SendMFA(config.Sniper, ticket, config.Password)
			if err != nil {
				logrus.Errorf("Failed to send MFA: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			mfaMutex.Lock()
			currentMFAToken = mfaToken
			mfaMutex.Unlock()

			// Save token to file immediately
			err = os.WriteFile("mfa.txt", []byte(mfaToken), 0644)
			if err != nil {
				logrus.Errorf("Failed to write MFA token to file: %v", err)
			} else {
				logrus.Infof("Successfully saved MFA token to file")
			}

			logrus.Infof("Successfully obtained new MFA token")
			time.Sleep(5 * time.Minute)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	logrus.Info("Shutting down MFA token monitor...")
}
