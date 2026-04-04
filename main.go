package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/robfig/cron/v3"
	"golang.org/x/crypto/bcrypt"
)

const puckServerPath = "/srv/puckserver/Puck.x86_64"
const passwordFilePath = "/srv/puckserver/.puckerup_password"
const configBasePath = "/srv/puckserver"
const schedulesFilePath = "/srv/puckserver/schedules.json"
const playerDataBasePath = "/srv/puckserver/UserData"
const sessionName = "speedup-session"

var serverStatusFilePatterns = []string{
	filepath.Join(configBasePath, "server%s-status.json"),
	filepath.Join(configBasePath, "server%s_status.json"),
	filepath.Join(configBasePath, "server%s.status.json"),
	filepath.Join(configBasePath, "server%s", "status.json"),
	filepath.Join(configBasePath, "server%s", "players.json"),
}

// --- Server Config Structures to match the JSON file ---
type PhaseDurationMap struct {
	Warmup     int `json:"Warmup"`
	FaceOff    int `json:"FaceOff"`
	Playing    int `json:"Playing"`
	BlueScore  int `json:"BlueScore"`
	RedScore   int `json:"RedScore"`
	Replay     int `json:"Replay"`
	PeriodOver int `json:"PeriodOver"`
	GameOver   int `json:"GameOver"`
}
type Mod struct {
	ID             int64 `json:"id"`
	Enabled        bool  `json:"enabled"`
	ClientRequired bool  `json:"clientRequired"`
}
type ServerConfig struct {
	Port                  int              `json:"port"`
	PingPort              int              `json:"pingPort"`
	Name                  string           `json:"name"`
	MaxPlayers            int              `json:"maxPlayers"`
	Password              string           `json:"password"`
	Voip                  bool             `json:"voip"`
	IsPublic              bool             `json:"isPublic"`
	AdminSteamIds         []string         `json:"adminSteamIds"`
	ReloadBannedSteamIds  bool             `json:"reloadBannedSteamIds"`
	UsePuckBannedSteamIds bool             `json:"usePuckBannedSteamIds"`
	PrintMetrics          bool             `json:"printMetrics"`
	KickTimeout           int              `json:"kickTimeout"`
	SleepTimeout          int              `json:"sleepTimeout"`
	JoinMidMatchDelay     int              `json:"joinMidMatchDelay"`
	TargetFrameRate       int              `json:"targetFrameRate"`
	ServerTickRate        int              `json:"serverTickRate"`
	ClientTickRate        int              `json:"clientTickRate"`
	StartPaused           bool             `json:"startPaused"`
	AllowVoting           bool             `json:"allowVoting"`
	PhaseDurationMap      PhaseDurationMap `json:"phaseDurationMap"`
	Mods                  []Mod            `json:"mods"`
}

type PlayerStats struct {
	MMR    int `json:"mmr"`
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
}

type PlayerMMRFile struct {
	Players map[string]PlayerStats `json:"players"`
}

type PlayerMMRRecord struct {
	SteamID string `json:"steamId"`
	MMR     int    `json:"mmr"`
	Wins    int    `json:"wins"`
	Losses  int    `json:"losses"`
}

type OnlinePlayer struct {
	SteamID string `json:"steamId,omitempty"`
	Name    string `json:"name,omitempty"`
	MMR     *int   `json:"mmr,omitempty"`
	Wins    *int   `json:"wins,omitempty"`
	Losses  *int   `json:"losses,omitempty"`
}

type ServerStatus struct {
	ServerNum     string         `json:"serverNum"`
	ServerName    string         `json:"serverName"`
	PlayersOnline int            `json:"playersOnline"`
	MaxPlayers    int            `json:"maxPlayers"`
	ServiceStatus string         `json:"serviceStatus"`
	Players       []OnlinePlayer `json:"players,omitempty"`
}

type ServersStatusResponse struct {
	TotalOnlinePlayers int            `json:"totalOnlinePlayers"`
	Servers            []ServerStatus `json:"servers"`
}

type ServerActivity struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type ServerActivityResponse struct {
	Servers []ServerActivity `json:"servers"`
}

type serverDefinition struct {
	Number string
	Config ServerConfig
}

// --- NEW: Rate Limiter ---
type loginAttempt struct {
	failures int
	lastTry  time.Time
}

var (
	loginAttempts = make(map[string]loginAttempt)
	loginMutex    = &sync.Mutex{}
	serverConfigMu = &sync.Mutex{}
)

const (
	maxLoginFailures = 5
	lockoutDuration  = 10 * time.Minute
)

// Use a secure key for session encryption.
var store = sessions.NewCookieStore([]byte("a-very-secret-and-secure-key-32-bytes-long-!"))

// --- Scheduler ---
type Schedule struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"` // HH:mm format
}

var (
	scheduler    = cron.New()
	scheduleMap  = make(map[string]Schedule)
	scheduleLock = &sync.Mutex{}
)

func init() {
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: true,
		Secure:   false, // Set to true if deploying with HTTPS
		SameSite: http.SameSiteLaxMode,
	}

	// Periodically clean up old login attempt entries
	go func() {
		for range time.Tick(30 * time.Minute) {
			loginMutex.Lock()
			for ip, attempt := range loginAttempts {
				if time.Since(attempt.lastTry) > lockoutDuration*2 { // Clean up entries older than 2x lockout
					delete(loginAttempts, ip)
				}
			}
			loginMutex.Unlock()
		}
	}()
}

func loadServerDefinitions() ([]serverDefinition, error) {
	configFiles, err := filepath.Glob(filepath.Join(configBasePath, "server*.json"))
	if err != nil {
		return nil, err
	}

	servers := make([]serverDefinition, 0, len(configFiles))
	for _, configFile := range configFiles {
		baseName := filepath.Base(configFile)
		serverNum := strings.TrimSuffix(strings.TrimPrefix(baseName, "server"), ".json")
		if _, err := strconv.Atoi(serverNum); err != nil {
			continue
		}

		fileContents, err := os.ReadFile(configFile)
		if err != nil {
			log.Printf("Failed to read %s: %v", configFile, err)
			continue
		}

		var config ServerConfig
		if err := json.Unmarshal(fileContents, &config); err != nil {
			log.Printf("Failed to parse %s: %v", configFile, err)
			continue
		}

		servers = append(servers, serverDefinition{Number: serverNum, Config: config})
	}

	sort.Slice(servers, func(i, j int) bool {
		left, _ := strconv.Atoi(servers[i].Number)
		right, _ := strconv.Atoi(servers[j].Number)
		return left < right
	})

	return servers, nil
}

func cloneServerConfig(base ServerConfig) ServerConfig {
	cloned := base
	cloned.AdminSteamIds = append([]string(nil), base.AdminSteamIds...)
	cloned.Mods = append([]Mod(nil), base.Mods...)
	return cloned
}

func defaultServerConfig() ServerConfig {
	return ServerConfig{
		Name:                  "Puck Server 1",
		MaxPlayers:            10,
		Voip:                  false,
		IsPublic:              true,
		AdminSteamIds:         []string{},
		ReloadBannedSteamIds:  true,
		UsePuckBannedSteamIds: true,
		PrintMetrics:          true,
		KickTimeout:           1800,
		SleepTimeout:          900,
		JoinMidMatchDelay:     10,
		TargetFrameRate:       380,
		ServerTickRate:        360,
		ClientTickRate:        360,
		StartPaused:           false,
		AllowVoting:           true,
		PhaseDurationMap: PhaseDurationMap{
			Warmup:     600,
			FaceOff:    3,
			Playing:    300,
			BlueScore:  5,
			RedScore:   5,
			Replay:     10,
			PeriodOver: 15,
			GameOver:   15,
		},
		Mods: []Mod{
			{ID: 3497097214, Enabled: true, ClientRequired: false},
			{ID: 3497344177, Enabled: true, ClientRequired: false},
			{ID: 3503065207, Enabled: true, ClientRequired: true},
		},
	}
}

func nextServerNumber(servers []serverDefinition) int {
	if len(servers) == 0 {
		return 1
	}

	lastServerNum, err := strconv.Atoi(servers[len(servers)-1].Number)
	if err != nil {
		return 1
	}

	return lastServerNum + 1
}

func nextServerPorts(servers []serverDefinition, candidateServerNum int) (int, int) {
	usedPorts := make(map[int]bool, len(servers)*2)
	for _, server := range servers {
		if server.Config.Port > 0 {
			usedPorts[server.Config.Port] = true
		}
		if server.Config.PingPort > 0 {
			usedPorts[server.Config.PingPort] = true
		}
	}

	port := 7777 + ((candidateServerNum - 1) * 2)
	for usedPorts[port] || usedPorts[port+1] {
		port += 2
	}

	return port, port + 1
}

func buildNewServerConfig(servers []serverDefinition, candidateServerNum int) ServerConfig {
	config := defaultServerConfig()
	if len(servers) > 0 {
		config = cloneServerConfig(servers[len(servers)-1].Config)
	}

	port, pingPort := nextServerPorts(servers, candidateServerNum)
	config.Name = fmt.Sprintf("Puck Server %d", candidateServerNum)
	config.Port = port
	config.PingPort = pingPort

	if config.AdminSteamIds == nil {
		config.AdminSteamIds = []string{}
	}
	if config.Mods == nil {
		config.Mods = []Mod{}
	}

	return config
}

func writeServerConfig(configFile string, config ServerConfig) error {
	if err := os.MkdirAll(filepath.Dir(configFile), 0755); err != nil {
		return err
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		return err
	}

	return os.WriteFile(configFile, buffer.Bytes(), 0644)
}

func getServiceStatus(serverNum string) string {
	cmd := exec.Command("systemctl", "is-active", fmt.Sprintf("puck@server%s", serverNum))
	statusOutput, err := cmd.Output()
	if err != nil {
		return "unknown"
	}

	status := strings.TrimSpace(string(statusOutput))
	if status == "" {
		return "unknown"
	}

	return status
}

func normalizeServerActivityStatus(serviceStatus string) string {
	if strings.EqualFold(strings.TrimSpace(serviceStatus), "active") {
		return "Active"
	}

	return "Inactive"
}

func loadPlayerMMRData() ([]PlayerMMRRecord, map[string]PlayerMMRRecord, error) {
	if _, err := os.Stat(playerDataBasePath); err != nil {
		if os.IsNotExist(err) {
			return []PlayerMMRRecord{}, map[string]PlayerMMRRecord{}, nil
		}
		return nil, nil, err
	}

	playersBySteamID := make(map[string]PlayerMMRRecord)
	err := filepath.WalkDir(playerDataBasePath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			return nil
		}

		fileContents, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Failed to read player data file %s: %v", path, err)
			return nil
		}

		var playerFile PlayerMMRFile
		if err := json.Unmarshal(fileContents, &playerFile); err != nil {
			log.Printf("Failed to parse player data file %s: %v", path, err)
			return nil
		}

		for steamID, stats := range playerFile.Players {
			playersBySteamID[steamID] = PlayerMMRRecord{
				SteamID: steamID,
				MMR:     stats.MMR,
				Wins:    stats.Wins,
				Losses:  stats.Losses,
			}
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	players := make([]PlayerMMRRecord, 0, len(playersBySteamID))
	for _, player := range playersBySteamID {
		players = append(players, player)
	}
	sortPlayerMMRRecords(players, "mmr")

	return players, playersBySteamID, nil
}

func sortPlayerMMRRecords(players []PlayerMMRRecord, sortBy string) {
	switch strings.ToLower(sortBy) {
	case "wins":
		sort.Slice(players, func(i, j int) bool {
			if players[i].Wins == players[j].Wins {
				return players[i].SteamID < players[j].SteamID
			}
			return players[i].Wins > players[j].Wins
		})
	case "losses":
		sort.Slice(players, func(i, j int) bool {
			if players[i].Losses == players[j].Losses {
				return players[i].SteamID < players[j].SteamID
			}
			return players[i].Losses > players[j].Losses
		})
	case "steamid":
		sort.Slice(players, func(i, j int) bool {
			return players[i].SteamID < players[j].SteamID
		})
	default:
		sort.Slice(players, func(i, j int) bool {
			if players[i].MMR == players[j].MMR {
				return players[i].SteamID < players[j].SteamID
			}
			return players[i].MMR > players[j].MMR
		})
	}
}

func filterPlayerMMRRecords(players []PlayerMMRRecord, search string) []PlayerMMRRecord {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return players
	}

	filtered := make([]PlayerMMRRecord, 0, len(players))
	for _, player := range players {
		if strings.Contains(strings.ToLower(player.SteamID), search) {
			filtered = append(filtered, player)
		}
	}

	return filtered
}

func findServerStatusFile(serverNum string) string {
	for _, pattern := range serverStatusFilePatterns {
		candidate := fmt.Sprintf(pattern, serverNum)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func extractIntByKeys(raw map[string]interface{}, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}

		switch typed := value.(type) {
		case float64:
			return int(typed), true
		case string:
			parsed, err := strconv.Atoi(typed)
			if err == nil {
				return parsed, true
			}
		}
	}

	return 0, false
}

func stringFromAnyKeys(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}

		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
	}

	return ""
}

func enrichOnlinePlayer(player *OnlinePlayer, mmrBySteamID map[string]PlayerMMRRecord) {
	if player.SteamID == "" {
		return
	}

	stats, ok := mmrBySteamID[player.SteamID]
	if !ok {
		return
	}

	mmr := stats.MMR
	wins := stats.Wins
	losses := stats.Losses
	player.MMR = &mmr
	player.Wins = &wins
	player.Losses = &losses
}

func extractOnlinePlayers(raw map[string]interface{}, mmrBySteamID map[string]PlayerMMRRecord) []OnlinePlayer {
	for _, key := range []string{"players", "playerList", "onlinePlayers", "currentPlayers"} {
		value, ok := raw[key]
		if !ok {
			continue
		}

		items, ok := value.([]interface{})
		if !ok {
			continue
		}

		players := make([]OnlinePlayer, 0, len(items))
		for _, item := range items {
			switch typed := item.(type) {
			case string:
				player := OnlinePlayer{SteamID: typed, Name: typed}
				enrichOnlinePlayer(&player, mmrBySteamID)
				players = append(players, player)
			case map[string]interface{}:
				player := OnlinePlayer{
					SteamID: stringFromAnyKeys(typed, "steamId", "steamID", "playerId", "id"),
					Name:    stringFromAnyKeys(typed, "name", "displayName", "playerName"),
				}
				if player.Name == "" {
					player.Name = player.SteamID
				}
				if player.SteamID == "" && player.Name == "" {
					continue
				}
				enrichOnlinePlayer(&player, mmrBySteamID)
				players = append(players, player)
			}
		}

		sort.Slice(players, func(i, j int) bool {
			leftMMR := -1
			rightMMR := -1
			if players[i].MMR != nil {
				leftMMR = *players[i].MMR
			}
			if players[j].MMR != nil {
				rightMMR = *players[j].MMR
			}
			if leftMMR == rightMMR {
				return players[i].Name < players[j].Name
			}
			return leftMMR > rightMMR
		})

		return players
	}

	return nil
}

func loadServerRuntimeData(serverNum string, mmrBySteamID map[string]PlayerMMRRecord) (int, int, []OnlinePlayer, bool) {
	statusFile := findServerStatusFile(serverNum)
	if statusFile == "" {
		return 0, 0, nil, false
	}

	fileContents, err := os.ReadFile(statusFile)
	if err != nil {
		log.Printf("Failed to read server status file %s: %v", statusFile, err)
		return 0, 0, nil, false
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(fileContents, &raw); err != nil {
		log.Printf("Failed to parse server status file %s: %v", statusFile, err)
		return 0, 0, nil, false
	}

	players := extractOnlinePlayers(raw, mmrBySteamID)
	playersOnline, foundOnlineCount := extractIntByKeys(raw, "playersOnline", "playerCount", "onlineCount", "onlinePlayers", "currentPlayers", "currentPlayerCount")
	if !foundOnlineCount && len(players) > 0 {
		playersOnline = len(players)
		foundOnlineCount = true
	}

	maxPlayers, _ := extractIntByKeys(raw, "maxPlayers", "capacity")

	return playersOnline, maxPlayers, players, foundOnlineCount || len(players) > 0
}

func isAuthenticatedSession(r *http.Request) bool {
	session, _ := store.Get(r, sessionName)
	auth, ok := session.Values["authenticated"].(bool)
	return ok && auth
}

func configuredServerAPIToken() string {
	for _, envName := range []string{"SPEEDUP_SERVER_API_TOKEN", "SPEEDUP_API_TOKEN", "PUCKERUP_API_TOKEN"} {
		token := strings.TrimSpace(os.Getenv(envName))
		if token != "" {
			return token
		}
	}

	return ""
}

func requestServerAPIToken(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader != "" && len(authHeader) > len("Bearer ") && strings.EqualFold(authHeader[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(authHeader[len("Bearer "):])
	}

	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

func hasValidServerAPIToken(r *http.Request) bool {
	configuredToken := configuredServerAPIToken()
	if configuredToken == "" {
		return false
	}

	requestToken := requestServerAPIToken(r)
	return requestToken != "" && requestToken == configuredToken
}

func activityAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hasValidServerAPIToken(r) || isAuthenticatedSession(r) {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

// --- Middleware for Authentication ---
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAuthenticatedSession(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			http.Redirect(w, r, "/login.html", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Login/Logout Handlers ---
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// --- NEW: Rate Limiting Check ---
	ip := strings.Split(r.RemoteAddr, ":")[0]
	loginMutex.Lock()
	attempt, ok := loginAttempts[ip]
	if ok && attempt.failures >= maxLoginFailures && time.Since(attempt.lastTry) < lockoutDuration {
		loginMutex.Unlock()
		log.Printf("Blocked login attempt from IP: %s", ip)
		http.Error(w, "Too many failed login attempts. Please try again later.", http.StatusTooManyRequests)
		return
	}
	loginMutex.Unlock()
	// --- END NEW ---

	var creds struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	hashBytes, err := os.ReadFile(passwordFilePath)
	if err != nil {
		log.Printf("Failed to read password file: %v", err)
		http.Error(w, "Server error during login", http.StatusInternalServerError)
		return
	}
	hash := strings.TrimSpace(string(hashBytes))

	log.Println("--- AUTHENTICATION ATTEMPT ---")
	log.Printf("Password received (length %d)", len(creds.Password))
	log.Printf("Hash from file    (length %d): \"%s\"", len(hash), hash)

	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(creds.Password))
	if err != nil {
		log.Println("Bcrypt comparison failed: password does not match hash.")
		// --- NEW: Record Failed Attempt ---
		loginMutex.Lock()
		attempt, _ := loginAttempts[ip]
		attempt.failures++
		attempt.lastTry = time.Now()
		loginAttempts[ip] = attempt
		loginMutex.Unlock()
		// --- END NEW ---
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// --- NEW: Clear Failed Attempts on Success ---
	loginMutex.Lock()
	delete(loginAttempts, ip)
	loginMutex.Unlock()
	// --- END NEW ---

	session, _ := store.Get(r, sessionName)
	session.Values["authenticated"] = true
	if err = session.Save(r, w); err != nil {
		log.Printf("Failed to save session: %v", err)
		http.Error(w, "Failed to save session", http.StatusInternalServerError)
		return
	}
	log.Println("Successful login")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"message": "Login successful"}`)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	session, _ := store.Get(r, sessionName)
	session.Values["authenticated"] = false

	// Create a new session options instance to avoid modifying the global one
	newOptions := *store.Options
	newOptions.MaxAge = -1 // Expire the cookie immediately
	session.Options = &newOptions

	session.Save(r, w)
	w.WriteHeader(http.StatusOK)
}

// --- API Handlers ---
func statusHandler(w http.ResponseWriter, r *http.Request) {
	_, err := os.Stat(puckServerPath)
	installed := !os.IsNotExist(err)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"installed": installed})
}

func installHandler(w http.ResponseWriter, r *http.Request) {
	time.Sleep(2 * time.Second)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Installation complete! The page will now reload."})
}

func getServerConfigHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverNum := vars["serverNum"]
	configFile := filepath.Join(configBasePath, fmt.Sprintf("server%s.json", serverNum))

	var config ServerConfig
	file, err := os.ReadFile(configFile)
	if err == nil {
		json.Unmarshal(file, &config)
	}

	status := getServiceStatus(serverNum)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"config": config, "status": status})
}

func updateServerConfigHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverNum := vars["serverNum"]
	configFile := filepath.Join(configBasePath, fmt.Sprintf("server%s.json", serverNum))

	var config ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := writeServerConfig(configFile, config); err != nil {
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Config saved successfully!"})
}

func createServerHandler(w http.ResponseWriter, r *http.Request) {
	serverConfigMu.Lock()
	defer serverConfigMu.Unlock()

	servers, err := loadServerDefinitions()
	if err != nil {
		log.Printf("Failed to load existing server configs: %v", err)
		http.Error(w, "Failed to inspect existing servers", http.StatusInternalServerError)
		return
	}

	candidateServerNum := nextServerNumber(servers)
	configFile := filepath.Join(configBasePath, fmt.Sprintf("server%d.json", candidateServerNum))
	for {
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			break
		}
		candidateServerNum++
		configFile = filepath.Join(configBasePath, fmt.Sprintf("server%d.json", candidateServerNum))
	}

	config := buildNewServerConfig(servers, candidateServerNum)
	if err := writeServerConfig(configFile, config); err != nil {
		log.Printf("Failed to create server config %s: %v", configFile, err)
		http.Error(w, "Failed to create server config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   fmt.Sprintf("Server %d created successfully!", candidateServerNum),
		"serverNum": candidateServerNum,
		"config":    config,
	})
}

func serverControlHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverNum := vars["serverNum"]
	var reqBody struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	serviceName := fmt.Sprintf("puck@server%s", serverNum)
	cmd := exec.Command("systemctl", reqBody.Action, serviceName)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to run systemctl %s %s: %v\nOutput: %s", reqBody.Action, serviceName, err, string(stderr))
		http.Error(w, fmt.Sprintf("Failed to %s server: %s", reqBody.Action, string(stderr)), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Command '%s' executed for server %s.", reqBody.Action, serverNum)})
}

func playersMMRHandler(w http.ResponseWriter, r *http.Request) {
	players, _, err := loadPlayerMMRData()
	if err != nil {
		log.Printf("Failed to load player MMR data: %v", err)
		http.Error(w, "Failed to load player data", http.StatusInternalServerError)
		return
	}

	players = filterPlayerMMRRecords(players, r.URL.Query().Get("search"))
	sortPlayerMMRRecords(players, r.URL.Query().Get("sort"))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(players)
}

func serverActivityHandler(w http.ResponseWriter, r *http.Request) {
	servers, err := loadServerDefinitions()
	if err != nil {
		log.Printf("Failed to load server definitions for activity endpoint: %v", err)
		http.Error(w, "Failed to load server configs", http.StatusInternalServerError)
		return
	}

	response := ServerActivityResponse{
		Servers: make([]ServerActivity, 0, len(servers)),
	}

	for _, server := range servers {
		serverName := strings.TrimSpace(server.Config.Name)
		if serverName == "" {
			serverName = fmt.Sprintf("Server %s", server.Number)
		}

		response.Servers = append(response.Servers, ServerActivity{
			Name:   serverName,
			Status: normalizeServerActivityStatus(getServiceStatus(server.Number)),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func serversStatusHandler(w http.ResponseWriter, r *http.Request) {
	servers, err := loadServerDefinitions()
	if err != nil {
		log.Printf("Failed to load server definitions: %v", err)
		http.Error(w, "Failed to load server configs", http.StatusInternalServerError)
		return
	}

	_, mmrBySteamID, err := loadPlayerMMRData()
	if err != nil {
		log.Printf("Failed to load player MMR data for server status: %v", err)
		http.Error(w, "Failed to load player data", http.StatusInternalServerError)
		return
	}

	response := ServersStatusResponse{
		Servers: make([]ServerStatus, 0, len(servers)),
	}

	for _, server := range servers {
		playersOnline, maxPlayers, players, _ := loadServerRuntimeData(server.Number, mmrBySteamID)
		if maxPlayers == 0 {
			maxPlayers = server.Config.MaxPlayers
		}
		if playersOnline == 0 && len(players) > 0 {
			playersOnline = len(players)
		}

		serverName := server.Config.Name
		if serverName == "" {
			serverName = fmt.Sprintf("Server %s", server.Number)
		}

		response.TotalOnlinePlayers += playersOnline
		response.Servers = append(response.Servers, ServerStatus{
			ServerNum:     server.Number,
			ServerName:    serverName,
			PlayersOnline: playersOnline,
			MaxPlayers:    maxPlayers,
			ServiceStatus: getServiceStatus(server.Number),
			Players:       players,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// --- Schedule Handlers ---
func getScheduleHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverNum := vars["serverNum"]
	scheduleLock.Lock()
	defer scheduleLock.Unlock()
	schedule, ok := scheduleMap[serverNum]
	if !ok {
		schedule = Schedule{Enabled: false, Time: "03:00"}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(schedule)
}

func updateScheduleHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverNum := vars["serverNum"]
	var schedule Schedule
	if err := json.NewDecoder(r.Body).Decode(&schedule); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	updateAndSaveSchedules(serverNum, schedule)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Schedule updated successfully!"})
}

func restartServerJob(serverNum string) func() {
	return func() {
		log.Printf("Executing scheduled restart for server %s...", serverNum)
		cmd := exec.Command("systemctl", "restart", fmt.Sprintf("puck@server%s", serverNum))
		err := cmd.Run()
		if err != nil {
			log.Printf("ERROR: Scheduled restart for server %s failed: %v", serverNum, err)
		} else {
			log.Printf("Scheduled restart for server %s completed.", serverNum)
		}
	}
}

func loadAndApplySchedules() {
	scheduleLock.Lock()
	defer scheduleLock.Unlock()
	log.Println("Loading and applying schedules...")

	file, err := os.ReadFile(schedulesFilePath)
	if err == nil {
		json.Unmarshal(file, &scheduleMap)
	}

	// Remove all old entries from the scheduler
	for _, entry := range scheduler.Entries() {
		scheduler.Remove(entry.ID)
	}

	for serverNum, schedule := range scheduleMap {
		if schedule.Enabled {
			t, err := time.Parse("15:04", schedule.Time)
			if err == nil {
				cronSpec := fmt.Sprintf("%d %d * * *", t.Minute(), t.Hour())
				id, err := scheduler.AddFunc(cronSpec, restartServerJob(serverNum))
				if err != nil {
					log.Printf("Error adding schedule for server %s: %v", serverNum, err)
				} else {
					log.Printf("Scheduled daily restart for server %s at %s UTC (Entry ID: %d)", serverNum, schedule.Time, id)
				}
			}
		}
	}
}

func updateAndSaveSchedules(serverNum string, schedule Schedule) {
	scheduleLock.Lock()
	defer scheduleLock.Unlock()
	scheduleMap[serverNum] = schedule
	data, err := json.MarshalIndent(scheduleMap, "", "  ")
	if err == nil {
		os.WriteFile(schedulesFilePath, data, 0644)
	}
	// Reload all schedules to apply the change
	go loadAndApplySchedules()
}

func main() {
	r := mux.NewRouter()

	// --- Public routes ---
	r.HandleFunc("/login", loginHandler).Methods("POST")
	r.HandleFunc("/logout", logoutHandler).Methods("POST")
	r.PathPrefix("/login.html").Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "login.html")
	}))
	r.Handle("/api/servers/activity", activityAuthMiddleware(http.HandlerFunc(serverActivityHandler))).Methods("GET")

	// --- Protected routes ---
	api := r.PathPrefix("/api").Subrouter()
	api.Use(authMiddleware)
	api.HandleFunc("/status", statusHandler)
	api.HandleFunc("/install", installHandler).Methods("POST")
	api.HandleFunc("/players/mmr", playersMMRHandler).Methods("GET")
	api.HandleFunc("/servers", createServerHandler).Methods("POST")
	api.HandleFunc("/servers/status", serversStatusHandler).Methods("GET")
	api.HandleFunc("/server/{serverNum}/config", getServerConfigHandler).Methods("GET")
	api.HandleFunc("/server/{serverNum}/config", updateServerConfigHandler).Methods("POST")
	api.HandleFunc("/server/{serverNum}/control", serverControlHandler).Methods("POST")
	api.HandleFunc("/server/{serverNum}/schedule", getScheduleHandler).Methods("GET")
	api.HandleFunc("/server/{serverNum}/schedule", updateScheduleHandler).Methods("POST")

	protectedFileServer := http.FileServer(http.Dir("."))
	r.PathPrefix("/").Handler(authMiddleware(protectedFileServer))

	// Load schedules from file and start the cron scheduler
	loadAndApplySchedules()
	scheduler.Start()

	port, exists := os.LookupEnv("SPEEDUP_PORT")
	if !exists {
		port, exists = os.LookupEnv("PUCKERUP_PORT")
	}
	if !exists {
		port = "8080"
	}
	
	listenAddr := ":" + port
	fmt.Printf("Starting SpeedUP server on http://0.0.0.0%s\n", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, r))
}

