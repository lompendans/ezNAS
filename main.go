package main

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/sha256"
	"crypto/hmac"
	"crypto/x509"
	"crypto/rsa"
	"crypto/x509/pkix"
	"math/big"
	"archive/zip"
	"encoding/pem"
	"context"
	"net/url"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"embed"
)

//go:embed web/*
var webFiles embed.FS

var version = "dev"

const appName = "ezNAS"
const defaultPort = "8080"
const maxUploadBytes int64 = 10 << 30

type Config struct {
	Host string `json:"host"`
	Port int `json:"port"`
	Storage string `json:"storage"`
	PasswordHash string `json:"password_hash,omitempty"`
	PasswordSalt string `json:"password_salt,omitempty"`
	TLSCert string `json:"tls_cert,omitempty"`
	TLSKey string `json:"tls_key,omitempty"`
	BackupDir string `json:"backup_dir,omitempty"`
}

type FileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Directory bool `json:"directory"`
	Size int64 `json:"size"`
	Modified time.Time `json:"modified"`
}

var shutdownRequests = make(chan struct{}, 1)

var (
	cfg Config
	cfgPath string
	cfgMu sync.RWMutex
	sessions = map[string]time.Time{}
	sessionMu sync.Mutex
	logger = log.New(os.Stdout, "ezNAS ", log.LstdFlags)
)

func main() {
	if err := loadConfig(); err != nil { logger.Fatal(err) }
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h": printHelp(); return
		case "version", "--version": fmt.Println("ezNAS " + version); return
		case "config": printConfig(); return
		case "storage": printStorage(); return
		case "status": checkStatus(); return
		case "stop": stopServer(); return
		case "update": runUpdate(); return
		case "tls": if len(args)>1 && args[1]=="generate" { generateTLSCertificate(); return }; fmt.Println("Usage: eznas tls generate"); return
		case "password":
			if len(args) > 1 && args[1] == "set" { setPassword(); return }
			fmt.Println("Usage: eznas password set"); os.Exit(2)
		case "start":
			// Start runs in the foreground. Use a service manager for background operation.
		default:
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0]); printHelp(); os.Exit(2)
		}
	}
	if err := ensurePasswordConfigured(); err != nil { logger.Fatalf("password setup: %v", err) }
	if err := os.MkdirAll(cfg.Storage, 0750); err != nil { logger.Fatalf("create storage: %v", err) }
	writePID()
	defer removePID()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", statusHandler)
	mux.HandleFunc("/api/login", loginHandler)
	mux.HandleFunc("/api/logout", logoutHandler)
	mux.Handle("/api/", authMiddleware(http.HandlerFunc(apiHandler)))
	static, err := fs.Sub(webFiles, "web")
	if err != nil { logger.Fatal(err) }
	mux.Handle("/", http.FileServer(http.FS(static)))
	server := &http.Server{
		Addr: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler: securityHeaders(mux),
		ReadHeaderTimeout: 10*time.Second,
		ReadTimeout: 0,
		WriteTimeout: 0,
		IdleTimeout: 90*time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	go func() {
		if cfg.TLSCert != "" || cfg.TLSKey != "" {
			if cfg.TLSCert == "" || cfg.TLSKey == "" { logger.Fatalf("both tls_cert and tls_key must be configured") }
			logger.Printf("web interface: https://%s", displayAddress(cfg.Host, cfg.Port))
			if err := server.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey); err != nil && !errors.Is(err, http.ErrServerClosed) { logger.Fatalf("server: %v", err) }
		} else {
			logger.Printf("web interface: http://%s", displayAddress(cfg.Host, cfg.Port))
			logger.Printf("WARNING: HTTPS is disabled. Configure TLS or use a trusted HTTPS reverse proxy for remote access.")
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) { logger.Fatalf("server: %v", err) }
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	select { case <-ch: case <-shutdownRequests: }
	ctx, cancel := contextWithTimeout(10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil { logger.Printf("shutdown: %v", err) }
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) { return context.WithTimeout(context.Background(), d) }

func configLocation() string {
	if p := os.Getenv("EZNAS_CONFIG"); p != "" { return p }
	d, err := os.UserConfigDir()
	if err != nil || d == "" { d = "." }
	return filepath.Join(d, "eznas", "config.json")
}

func loadConfig() error {
	cfgPath = configLocation()
	cfg = Config{Host:"0.0.0.0", Port:8080, Storage:filepath.Join(".", "eznas-data")}
	if b, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil { return fmt.Errorf("parse %s: %w", cfgPath, err) }
	} else if !os.IsNotExist(err) { return err }
	if cfg.Host == "" { cfg.Host = "0.0.0.0" }
	if cfg.Port < 1 || cfg.Port > 65535 { return fmt.Errorf("invalid port in config") }
	if cfg.Storage == "" { return fmt.Errorf("storage path cannot be empty") }
	if cfg.BackupDir == "" { cfg.BackupDir = filepath.Join(filepath.Dir(configLocation()), "backups") }
	if cfg.TLSCert != "" { cfg.TLSCert, _ = filepath.Abs(cfg.TLSCert) }
	if cfg.TLSKey != "" { cfg.TLSKey, _ = filepath.Abs(cfg.TLSKey) }
	if cfg.BackupDir != "" { cfg.BackupDir, _ = filepath.Abs(cfg.BackupDir) }
	abs, err := filepath.Abs(cfg.Storage); if err != nil { return err }; cfg.Storage = abs
	return nil
}

func saveConfig() error {
	cfgMu.RLock(); c := cfg; cfgMu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil { return err }
	b, err := json.MarshalIndent(c, "", "  "); if err != nil { return err }
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0600); err != nil { return err }
	return os.Rename(tmp, cfgPath)
}

func printHelp() {
	fmt.Println("ezNAS - turn a computer into a simple NAS")
	fmt.Println("Usage: eznas [command]")
	fmt.Println("  eznas              Start server in foreground")
	fmt.Println("  eznas start        Start server in foreground")
	fmt.Println("  eznas status       Check local server status")
	fmt.Println("  eznas stop         Stop a server started with eznas")
	fmt.Println("  eznas config       Show configuration location and safe settings")
	fmt.Println("  eznas storage      Show storage path and free space")
	fmt.Println("  eznas password set Set or change the required password")
	fmt.Println("  eznas update       Download and verify the latest stable release")
	fmt.Println("  eznas tls generate Generate a local self-signed HTTPS certificate")
	fmt.Println("  eznas version      Print version")
	fmt.Println("  eznas help         Show this help")
}

func printConfig() {
	fmt.Printf("Config file: %s\nHost: %s\nPort: %d\nStorage: %s\nPassword protection: %v\n", cfgPath, cfg.Host, cfg.Port, cfg.Storage, cfg.PasswordHash != "")
}
func printStorage() {
	if err := os.MkdirAll(cfg.Storage, 0750); err != nil { logger.Fatal(err) }
	info, err := getStorageCapacity(cfg.Storage)
	if err != nil {
		fmt.Printf("Storage path: %s\nFree-space reporting is unavailable on this platform: %v\n", cfg.Storage, err)
		return
	}
	fmt.Printf("Storage path: %s\nFree: %.2f GiB\nTotal: %.2f GiB\n", cfg.Storage, float64(info.Free)/(1<<30), float64(info.Total)/(1<<30))
}
func displayAddress(host string, port int) string {
	if host == "0.0.0.0" || host == "::" || host == "" { return "localhost:" + strconv.Itoa(port) }
	return net.JoinHostPort(host, strconv.Itoa(port))
}
func pidPath() string { return filepath.Join(filepath.Dir(cfgPath), "eznas.pid") }
func writePID() { _ = os.MkdirAll(filepath.Dir(cfgPath), 0700); _ = os.WriteFile(pidPath(), []byte(strconv.Itoa(os.Getpid())), 0600) }
func removePID() { _ = os.Remove(pidPath()) }
func stopServer() {
	b, err := os.ReadFile(pidPath()); if err != nil { fmt.Println("No ezNAS PID file found. Is the server running?"); return }
	pid, err := strconv.Atoi(strings.TrimSpace(string(b))); if err != nil { logger.Fatal("invalid PID file") }
	p, err := os.FindProcess(pid); if err != nil { logger.Fatal(err) }
	if err := p.Signal(os.Interrupt); err != nil { fmt.Printf("Could not stop PID %d: %v\n", pid, err); os.Exit(1) }
	fmt.Printf("Stop signal sent to ezNAS (PID %d).\n", pid)
}
func checkStatus() {
	url := "http://127.0.0.1:" + strconv.Itoa(cfg.Port) + "/api/status"
	client := http.Client{Timeout: 2*time.Second}
	r, err := client.Get(url); if err != nil { fmt.Println("ezNAS is not responding at " + url); os.Exit(1) }
	defer r.Body.Close()
	fmt.Printf("ezNAS is responding: %s\n", url)
}
func ensurePasswordConfigured() error {
	if cfg.PasswordHash != "" { return nil }
	// Secure first-run default: generate a high-entropy password and persist only its salted hash.
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil { return err }
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil { return err }
	password := base64.RawURLEncoding.EncodeToString(secret)
	cfg.PasswordSalt = base64.StdEncoding.EncodeToString(salt)
	cfg.PasswordHash = passwordHash(password, salt)
	if err := saveConfig(); err != nil { return err }
	fmt.Println()
	fmt.Println("SECURITY: ezNAS requires a password. A one-time password was generated for this first run:")
	fmt.Println(password)
	fmt.Println("Save it now. To change it later, stop ezNAS and run: eznas password set")
	fmt.Println()
	return nil
}

func setPassword() {
	fmt.Print("New password (input is visible in this basic terminal prompt): ")
	var p string
	if _, err := fmt.Scanln(&p); err != nil { logger.Fatal(err) }
	if len(p) < 10 { logger.Fatal("password must be at least 10 characters") }
	salt := make([]byte, 16); if _, err := rand.Read(salt); err != nil { logger.Fatal(err) }
	cfg.PasswordSalt = base64.StdEncoding.EncodeToString(salt)
	cfg.PasswordHash = passwordHash(p, salt)
	if err := saveConfig(); err != nil { logger.Fatal(err) }
	fmt.Println("Password protection enabled. Restart ezNAS to apply.")
}
func passwordHash(p string, salt []byte) string {
	// PBKDF2-HMAC-SHA256, 200,000 iterations, one 32-byte output block.
	mac := hmac.New(sha256.New, []byte(p))
	var counter [4]byte
	counter[3] = 1
	mac.Write(salt)
	mac.Write(counter[:])
	u := mac.Sum(nil)
	out := append([]byte(nil), u...)
	for i := 1; i < 200000; i++ {
		mac = hmac.New(sha256.New, []byte(p))
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range out { out[j] ^= u[j] }
	}
	return hex.EncodeToString(out)
}
func checkPassword(p string) bool {
	cfgMu.RLock(); hash, saltText := cfg.PasswordHash, cfg.PasswordSalt; cfgMu.RUnlock()
	salt, err := base64.StdEncoding.DecodeString(saltText); if err != nil { return false }
	got := passwordHash(p, salt)
	return subtle.ConstantTimeCompare([]byte(got), []byte(hash)) == 1
}
func hasSession(r *http.Request) bool {
	c, err := r.Cookie("eznas_session"); if err != nil { return false }
	sessionMu.Lock(); defer sessionMu.Unlock()
	exp, ok := sessions[c.Value]; if !ok { return false }
	if time.Now().After(exp) { delete(sessions, c.Value); return false }
	return true
}
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.RLock(); enabled := cfg.PasswordHash != ""; cfgMu.RUnlock()
		if !enabled || r.URL.Path == "/api/login" || r.URL.Path == "/api/status" { next.ServeHTTP(w,r); return }
		if hasSession(r) { next.ServeHTTP(w,r); return }
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error":"authentication required"})
	})
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options","nosniff")
		w.Header().Set("X-Frame-Options","DENY")
		w.Header().Set("Referrer-Policy","no-referrer")
		w.Header().Set("Content-Security-Policy","default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self'; script-src 'self'; connect-src 'self' https://api.github.com; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w,r)
	})
}
func statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { methodNotAllowed(w); return }
	cfgMu.RLock(); c := cfg; cfgMu.RUnlock()
	hostname, _ := os.Hostname()
	address := hostname + ":" + strconv.Itoa(c.Port)
	writeJSON(w, 200, map[string]any{"name":"ezNAS","version":version,"passwordRequired":c.PasswordHash!="","storage":c.Storage,"host":c.Host,"port":c.Port,"hostname":hostname,"address":address,"os":runtime.GOOS,"https":cfg.TLSCert!="" && cfg.TLSKey!="","backupDir":cfg.BackupDir})
}
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet { writeJSON(w,200,map[string]any{"passwordRequired":cfg.PasswordHash!=""}); return }
	if r.Method != http.MethodPost { methodNotAllowed(w); return }
	var in struct { Password string `json:"password"` }
	if err := json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20)).Decode(&in); err != nil { writeJSON(w,400,map[string]string{"error":"invalid request"}); return }
	if !checkPassword(in.Password) { writeJSON(w,401,map[string]string{"error":"incorrect password"}); return }
	token := make([]byte,32); if _, err := rand.Read(token); err != nil { writeJSON(w,500,map[string]string{"error":"session creation failed"}); return }
	value := base64.RawURLEncoding.EncodeToString(token)
	sessionMu.Lock(); sessions[value] = time.Now().Add(12*time.Hour); sessionMu.Unlock()
	http.SetCookie(w,&http.Cookie{Name:"eznas_session",Value:value,Path:"/",HttpOnly:true,SameSite:http.SameSiteStrictMode,Secure:r.TLS!=nil,MaxAge:43200})
	writeJSON(w,200,map[string]bool{"ok":true})
}
func logoutHandler(w http.ResponseWriter,r *http.Request) {
	if r.Method != http.MethodPost { methodNotAllowed(w); return }
	if c,err:=r.Cookie("eznas_session"); err==nil { sessionMu.Lock(); delete(sessions,c.Value); sessionMu.Unlock() }
	http.SetCookie(w,&http.Cookie{Name:"eznas_session",Value:"",Path:"/",HttpOnly:true,SameSite:http.SameSiteStrictMode,MaxAge:-1})
	writeJSON(w,200,map[string]bool{"ok":true})
}
func apiHandler(w http.ResponseWriter,r *http.Request) {
	switch r.URL.Path {
	case "/api/files":
		if r.Method!=http.MethodGet { methodNotAllowed(w); return }; listFiles(w,r)
	case "/api/upload":
		if r.Method!=http.MethodPost { methodNotAllowed(w); return }; uploadFile(w,r)
	case "/api/download":
		if r.Method!=http.MethodGet { methodNotAllowed(w); return }; downloadFile(w,r)
	case "/api/mkdir":
		if r.Method!=http.MethodPost { methodNotAllowed(w); return }; makeDirectory(w,r)
	case "/api/rename":
		if r.Method!=http.MethodPost { methodNotAllowed(w); return }; renamePath(w,r)
	case "/api/delete":
		if r.Method!=http.MethodPost { methodNotAllowed(w); return }; deletePath(w,r)
	case "/api/trash":
		if r.Method != http.MethodGet { methodNotAllowed(w); return }; listTrash(w,r)
	case "/api/trash/restore":
		if r.Method != http.MethodPost { methodNotAllowed(w); return }; restoreTrash(w,r)
	case "/api/trash/purge":
		if r.Method != http.MethodPost { methodNotAllowed(w); return }; purgeTrash(w,r)
	case "/api/backups":
		if r.Method == http.MethodGet { listBackups(w,r); return }
		if r.Method == http.MethodPost { createBackup(w,r); return }
		methodNotAllowed(w)
	case "/api/backups/download":
		if r.Method != http.MethodGet { methodNotAllowed(w); return }; downloadBackup(w,r)
	case "/api/search":
		if r.Method!=http.MethodGet { methodNotAllowed(w); return }; searchFiles(w,r)
	case "/api/storage":
		if r.Method!=http.MethodGet { methodNotAllowed(w); return }; storageInfo(w)
	case "/api/update":
		if r.Method != http.MethodPost { methodNotAllowed(w); return }
		updateLaunched = false
		updateStatusMessage = ""
		if err := stageLatestUpdate(); err != nil { writeJSON(w, 502, map[string]string{"error": err.Error()}); return }
		if !updateLaunched { writeJSON(w, 200, map[string]any{"ok": true, "updated": false, "message": updateStatusMessage}); return }
		writeJSON(w, 202, map[string]any{"ok": true, "updated": true, "message": updateStatusMessage})
		go func(){ time.Sleep(300*time.Millisecond); select { case shutdownRequests <- struct{}{}: default: } }()
	default: http.NotFound(w,r)
	}
}
func cleanRelative(p string) (string,error) {
	p = strings.ReplaceAll(p,"\\","/")
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p,"/") || (len(p)>=2 && p[1]==':') { return "", errors.New("absolute paths are not allowed") }
	c := filepath.Clean(filepath.FromSlash(p))
	if c=="." { return "",nil }
	if c==".." || strings.HasPrefix(c,".."+string(filepath.Separator)) { return "",errors.New("path escapes storage") }
	return c,nil
}
func safePath(rel string) (string,error) {
	c,err:=cleanRelative(rel); if err!=nil{return "",err}
	cfgMu.RLock(); root:=cfg.Storage; cfgMu.RUnlock()
	target:=filepath.Join(root,c)
	relCheck,err:=filepath.Rel(root,target); if err!=nil || relCheck==".." || strings.HasPrefix(relCheck,".."+string(filepath.Separator)) { return "",errors.New("path escapes storage") }
	// Existing symlinks must not let operations escape the storage root.
	cur:=target
	for {
		fi,e:=os.Lstat(cur)
		if e==nil && fi.Mode()&os.ModeSymlink!=0 {
			resolved,e:=filepath.EvalSymlinks(cur); if e!=nil{return "",e}
			r,e:=filepath.Rel(root,resolved); if e!=nil || r==".." || strings.HasPrefix(r,".."+string(filepath.Separator)){return "",errors.New("symlink escapes storage")}
		}
		if cur==root { break }; parent:=filepath.Dir(cur); if parent==cur { break }; cur=parent
	}
	return target,nil
}
func listFiles(w http.ResponseWriter,r *http.Request) {
	p,err:=safePath(r.URL.Query().Get("path")); if err!=nil { writeJSON(w,400,map[string]string{"error":err.Error()});return }
	entries,err:=os.ReadDir(p); if err!=nil { writeJSON(w,400,map[string]string{"error":err.Error()});return }
	out:=make([]FileInfo,0,len(entries))
	for _,e:=range entries { if e.Name()==".eznas-trash" { continue }; info,err:=e.Info();if err!=nil{continue}; rel,_:=filepath.Rel(cfg.Storage,filepath.Join(p,e.Name())); out=append(out,FileInfo{Name:e.Name(),Path:filepath.ToSlash(rel),Directory:e.IsDir(),Size:info.Size(),Modified:info.ModTime()}) }
	sort.Slice(out,func(i,j int)bool{if out[i].Directory!=out[j].Directory{return out[i].Directory};return strings.ToLower(out[i].Name)<strings.ToLower(out[j].Name)})
	writeJSON(w,200,map[string]any{"path":r.URL.Query().Get("path"),"items":out})
}
func uploadFile(w http.ResponseWriter,r *http.Request) {
	r.Body=http.MaxBytesReader(w,r.Body,maxUploadBytes+(1<<20))
	if err:=r.ParseMultipartForm(32<<20);err!=nil { writeJSON(w,400,map[string]string{"error":"upload too large or invalid multipart form"});return }
	dir,err:=safePath(r.FormValue("path"));if err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return}
	f,h,err:=r.FormFile("file");if err!=nil{writeJSON(w,400,map[string]string{"error":"missing file"});return};defer f.Close()
	name:=filepath.Base(strings.ReplaceAll(h.Filename,"\\","/"));if name=="."||name=="/"||name==""||strings.ContainsRune(name,0){writeJSON(w,400,map[string]string{"error":"invalid filename"});return}
	target,err:=safePath(filepath.ToSlash(filepath.Join(r.FormValue("path"),name)));if err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return}
	if _,err:=os.Lstat(target);err==nil{writeJSON(w,409,map[string]string{"error":"a file with that name already exists"});return}
	tmp,err:=os.CreateTemp(dir,".eznas-upload-*");if err!=nil{writeJSON(w,500,map[string]string{"error":"cannot create upload"});return}
	ok:=false;defer func(){tmp.Close();if !ok{os.Remove(tmp.Name())}}()
	if _,err=io.Copy(tmp,io.LimitReader(f,maxUploadBytes+1));err!=nil{writeJSON(w,500,map[string]string{"error":"upload failed"});return}
	st,err:=tmp.Stat();if err!=nil||st.Size()>maxUploadBytes{writeJSON(w,413,map[string]string{"error":"file exceeds 10 GiB limit"});return}
	if err=tmp.Sync();err!=nil{writeJSON(w,500,map[string]string{"error":"cannot flush upload"});return};if err=tmp.Close();err!=nil{writeJSON(w,500,map[string]string{"error":"cannot close upload"});return}
	if err=os.Rename(tmp.Name(),target);err!=nil{writeJSON(w,500,map[string]string{"error":"cannot finish upload"});return};ok=true
	writeJSON(w,201,map[string]any{"ok":true,"name":name,"size":st.Size()})
}
func downloadFile(w http.ResponseWriter,r *http.Request) {
	p,err:=safePath(r.URL.Query().Get("path"));if err!=nil{http.Error(w,"invalid path",400);return}
	fi,err:=os.Stat(p);if err!=nil||fi.IsDir(){http.Error(w,"file not found",404);return}
	w.Header().Set("Content-Disposition","attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(p)))
	http.ServeFile(w,r,p)
}
func makeDirectory(w http.ResponseWriter,r *http.Request) {
	var in struct{Path string `json:"path"`;Name string `json:"name"`}
	if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20)).Decode(&in);err!=nil{writeJSON(w,400,map[string]string{"error":"invalid request"});return}
	name:=strings.TrimSpace(in.Name);if name==""||name=="."||name==".."||strings.ContainsAny(name,"/\\") {writeJSON(w,400,map[string]string{"error":"invalid folder name"});return}
	p,err:=safePath(filepath.ToSlash(filepath.Join(in.Path,name)));if err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return}
	if err=os.Mkdir(p,0750);err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return};writeJSON(w,201,map[string]bool{"ok":true})
}
func renamePath(w http.ResponseWriter,r *http.Request) {
	var in struct{Path string `json:"path"`;Name string `json:"name"`}
	if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20)).Decode(&in);err!=nil{writeJSON(w,400,map[string]string{"error":"invalid request"});return}
	src,err:=safePath(in.Path);if err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return}
	name:=strings.TrimSpace(in.Name);if name==""||name=="."||name==".."||strings.ContainsAny(name,"/\\") {writeJSON(w,400,map[string]string{"error":"invalid name"});return}
	dst,err:=safePath(filepath.ToSlash(filepath.Join(filepath.Dir(in.Path),name)));if err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return}
	if _,err=os.Lstat(dst);err==nil{writeJSON(w,409,map[string]string{"error":"destination already exists"});return}
	if err=os.Rename(src,dst);err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return};writeJSON(w,200,map[string]bool{"ok":true})
}
func deletePath(w http.ResponseWriter,r *http.Request) {
	var in struct{Path string `json:"path"`}
	if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20)).Decode(&in);err!=nil{writeJSON(w,400,map[string]string{"error":"invalid request"});return}
	p,err:=safePath(in.Path);if err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return}
	cfgMu.RLock();root:=cfg.Storage;cfgMu.RUnlock()
	if p==root{writeJSON(w,400,map[string]string{"error":"cannot delete the storage root"});return}
	if err = moveToTrash(p, root); err != nil { writeJSON(w,400,map[string]string{"error":err.Error()}); return }
	writeJSON(w,200,map[string]any{"ok":true,"message":"Moved to recycle bin"})
}
func searchFiles(w http.ResponseWriter,r *http.Request) {
	q:=strings.TrimSpace(r.URL.Query().Get("q"));if q==""{writeJSON(w,200,map[string]any{"items":[]FileInfo{}});return}
	if len(q)>200{writeJSON(w,400,map[string]string{"error":"query too long"});return}
	cfgMu.RLock();root:=cfg.Storage;cfgMu.RUnlock();out:=[]FileInfo{};limit:=500
	err:=filepath.WalkDir(root,func(p string,d fs.DirEntry,err error)error{
		if err!=nil{return nil};if p==root{return nil}
		if filepath.Base(p)==".eznas-trash" && d.IsDir(){return filepath.SkipDir}
		if d.Type()&os.ModeSymlink!=0{return filepath.SkipDir}
		if strings.Contains(strings.ToLower(d.Name()),strings.ToLower(q)){info,e:=d.Info();if e==nil{rel,_:=filepath.Rel(root,p);out=append(out,FileInfo{Name:d.Name(),Path:filepath.ToSlash(rel),Directory:d.IsDir(),Size:info.Size(),Modified:info.ModTime()});if len(out)>=limit{return errors.New("limit")}}}
		return nil
	})
	if err!=nil&&err.Error()!="limit"{writeJSON(w,500,map[string]string{"error":"search failed"});return}
	writeJSON(w,200,map[string]any{"items":out,"limited":len(out)>=limit})
}
func storageInfo(w http.ResponseWriter) {
	cfgMu.RLock();root:=cfg.Storage;cfgMu.RUnlock()
	info, err := getStorageCapacity(root)
	if err != nil { writeJSON(w,200,map[string]any{"path":root,"note":"Disk capacity unavailable on this platform"}); return }
	writeJSON(w,200,map[string]any{"path":root,"freeBytes":info.Free,"totalBytes":info.Total,"usedBytes":info.Total-info.Free})
}
func writeJSON(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json; charset=utf-8");w.WriteHeader(status);_ = json.NewEncoder(w).Encode(v)}
func methodNotAllowed(w http.ResponseWriter){writeJSON(w,http.StatusMethodNotAllowed,map[string]string{"error":"method not allowed"})}


type TrashEntry struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Directory bool `json:"directory"`
	Size int64 `json:"size"`
	Deleted time.Time `json:"deleted"`
}
type trashMetadata struct { Original string `json:"original"`; Name string `json:"name"`; Deleted time.Time `json:"deleted"` }
func trashDir() string { cfgMu.RLock(); root:=cfg.Storage; cfgMu.RUnlock(); return filepath.Join(root,".eznas-trash") }
func moveToTrash(p, root string) error {
	rel,err:=filepath.Rel(root,p); if err!=nil || rel=="." || strings.HasPrefix(rel,".."+string(filepath.Separator)) { return errors.New("invalid recycle-bin path") }
	dir:=trashDir(); if err=os.MkdirAll(dir,0700); err!=nil{return err}
	fi,err:=os.Lstat(p); if err!=nil{return err}; if fi.Mode()&os.ModeSymlink!=0{return errors.New("symbolic links cannot be moved to recycle bin")}
	id:=fmt.Sprintf("%d-%s",time.Now().UnixNano(),filepath.Base(p))
	dst:=filepath.Join(dir,id)
	if err=os.Rename(p,dst);err!=nil{return err}
	meta:=trashMetadata{Original:filepath.ToSlash(rel),Name:filepath.Base(p),Deleted:time.Now()}
	b,err:=json.Marshal(meta);if err!=nil{_ = os.Rename(dst,p);return err}
	if err=os.WriteFile(dst+".json",b,0600);err!=nil{_ = os.Rename(dst,p);return err}
	return nil
}
func listTrash(w http.ResponseWriter,r *http.Request) {
	dir:=trashDir(); entries,err:=os.ReadDir(dir);if os.IsNotExist(err){writeJSON(w,200,map[string]any{"items":[]TrashEntry{}});return};if err!=nil{writeJSON(w,500,map[string]string{"error":"cannot read recycle bin"});return}
	out:=[]TrashEntry{}
	for _,e:=range entries { if e.IsDir() || strings.HasSuffix(e.Name(),".json"){continue}; b,er:=os.ReadFile(filepath.Join(dir,e.Name())+".json");if er!=nil{continue};var m trashMetadata;if json.Unmarshal(b,&m)!=nil{continue};fi,er:=e.Info();if er!=nil{continue};out=append(out,TrashEntry{ID:e.Name(),Name:m.Name,Path:m.Original,Directory:fi.IsDir(),Size:fi.Size(),Deleted:m.Deleted}) }
	sort.Slice(out,func(i,j int)bool{return out[i].Deleted.After(out[j].Deleted)})
	writeJSON(w,200,map[string]any{"items":out})
}
func restoreTrash(w http.ResponseWriter,r *http.Request) {
	var in struct{ID string `json:"id"`};if json.NewDecoder(http.MaxBytesReader(w,r.Body,1<<20)).Decode(&in)!=nil || in.ID=="" || strings.ContainsAny(in.ID,"/\\"){writeJSON(w,400,map[string]string{"error":"invalid recycle-bin item"});return}
	dir:=trashDir();src:=filepath.Join(dir,in.ID);b,err:=os.ReadFile(src+".json");if err!=nil{writeJSON(w,404,map[string]string{"error":"recycle-bin item not found"});return}
	var m trashMetadata;if json.Unmarshal(b,&m)!=nil{writeJSON(w,500,map[string]string{"error":"invalid recycle-bin metadata"});return}
	dst,err:=safePath(m.Original);if err!=nil{writeJSON(w,400,map[string]string{"error":"original path is no longer safe"});return}
	if _,err=os.Lstat(dst);err==nil{writeJSON(w,409,map[string]string{"error":"original path already exists; rename or move the existing item first"});return}
	if err=os.MkdirAll(filepath.Dir(dst),0750);err!=nil{writeJSON(w,500,map[string]string{"error":"cannot recreate original folder"});return}
	if err=os.Rename(src,dst);err!=nil{writeJSON(w,500,map[string]string{"error":"restore failed"});return};_ = os.Remove(src+".json")
	writeJSON(w,200,map[string]any{"ok":true,"path":m.Original})
}
func purgeTrash(w http.ResponseWriter,r *http.Request) {
	dir:=trashDir();entries,err:=os.ReadDir(dir);if os.IsNotExist(err){writeJSON(w,200,map[string]bool{"ok":true});return};if err!=nil{writeJSON(w,500,map[string]string{"error":"cannot read recycle bin"});return}
	for _,e:=range entries { if err=os.RemoveAll(filepath.Join(dir,e.Name()));err!=nil{writeJSON(w,500,map[string]string{"error":"could not completely empty recycle bin"});return} }
	writeJSON(w,200,map[string]bool{"ok":true})
}
type BackupInfo struct { Name string `json:"name"`; Size int64 `json:"size"`; Created time.Time `json:"created"` }
func listBackups(w http.ResponseWriter,r *http.Request) {
	cfgMu.RLock();dir:=cfg.BackupDir;cfgMu.RUnlock()
	entries,err:=os.ReadDir(dir);if os.IsNotExist(err){writeJSON(w,200,map[string]any{"items":[]BackupInfo{}});return};if err!=nil{writeJSON(w,500,map[string]string{"error":"cannot read backups"});return}
	out:=[]BackupInfo{};for _,e:=range entries {if e.IsDir()||!strings.HasSuffix(e.Name(),".zip"){continue};fi,er:=e.Info();if er!=nil{continue};out=append(out,BackupInfo{Name:e.Name(),Size:fi.Size(),Created:fi.ModTime()})}
	sort.Slice(out,func(i,j int)bool{return out[i].Created.After(out[j].Created)});writeJSON(w,200,map[string]any{"items":out})
}
func createBackup(w http.ResponseWriter,r *http.Request) {
	cfgMu.RLock();root,dir:=cfg.Storage,cfg.BackupDir;cfgMu.RUnlock()
	relBackup, relErr := filepath.Rel(root, dir)
	if relErr == nil && relBackup != ".." && !strings.HasPrefix(relBackup, ".."+string(filepath.Separator)) {
		writeJSON(w,400,map[string]string{"error":"backup_dir must be outside the storage directory to prevent recursive backups"}); return
	}
	if err:=os.MkdirAll(dir,0700);err!=nil{writeJSON(w,500,map[string]string{"error":"cannot create backup directory"});return}
	name:="eznas-backup-"+time.Now().Format("20060102-150405")+".zip";target:=filepath.Join(dir,name);tmp:=target+".tmp"
	f,err:=os.OpenFile(tmp,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if err!=nil{writeJSON(w,500,map[string]string{"error":"cannot create backup file"});return}
	zw:=zip.NewWriter(f)
	err=filepath.WalkDir(root,func(p string,d fs.DirEntry,walkErr error)error{
		if walkErr!=nil{return walkErr};if p==root{return nil}
		rel,er:=filepath.Rel(root,p);if er!=nil{return er}
		if rel==".eznas-trash"{if d.IsDir(){return filepath.SkipDir};return nil}
		if d.Type()&os.ModeSymlink!=0 {if d.IsDir(){return filepath.SkipDir};return nil}
		fi,er:=d.Info();if er!=nil{return er}
		h,er:=zip.FileInfoHeader(fi);if er!=nil{return er};h.Name=filepath.ToSlash(rel);if fi.IsDir(){h.Name+="/"}else{h.Method=zip.Deflate}
		writer,er:=zw.CreateHeader(h);if er!=nil{return er}
		if fi.IsDir(){return nil}
		in,er:=os.Open(p);if er!=nil{return er};_,copyErr:=io.Copy(writer,in);closeErr:=in.Close();if copyErr!=nil{return copyErr};return closeErr
	})
	closeZipErr:=zw.Close();closeFileErr:=f.Close();if err==nil{err=closeZipErr};if err==nil{err=closeFileErr}
	if err!=nil{_ = os.Remove(tmp);writeJSON(w,500,map[string]string{"error":"backup failed; check available disk space and permissions"});return}
	if err=os.Rename(tmp,target);err!=nil{_ = os.Remove(tmp);writeJSON(w,500,map[string]string{"error":"cannot finalize backup"});return}
	fi,err:=os.Stat(target);if err!=nil{writeJSON(w,500,map[string]string{"error":"backup created but metadata could not be read"});return}
	writeJSON(w,201,map[string]any{"ok":true,"backup":BackupInfo{Name:name,Size:fi.Size(),Created:fi.ModTime()}})
}
func downloadBackup(w http.ResponseWriter,r *http.Request) {
	name:=r.URL.Query().Get("name");if name==""||filepath.Base(name)!=name||!strings.HasSuffix(name,".zip"){http.Error(w,"invalid backup name",400);return}
	cfgMu.RLock();p:=filepath.Join(cfg.BackupDir,name);cfgMu.RUnlock()
	fi,err:=os.Stat(p);if err!=nil||fi.IsDir(){http.NotFound(w,r);return}
	w.Header().Set("Content-Disposition","attachment; filename="+strconv.Quote(name));http.ServeFile(w,r,p)
}
func generateTLSCertificate() {
	dir:=filepath.Dir(cfgPath);if err:=os.MkdirAll(dir,0700);err!=nil{logger.Fatal(err)}
	certPath:=filepath.Join(dir,"tls-cert.pem");keyPath:=filepath.Join(dir,"tls-key.pem")
	key,err:=rsa.GenerateKey(rand.Reader,3072);if err!=nil{logger.Fatal(err)}
	host,_:=os.Hostname();if host==""{host="localhost"}
	now:=time.Now()
	template:=x509.Certificate{SerialNumber:big.NewInt(now.UnixNano()),Subject:pkix.Name{CommonName:host},NotBefore:now.Add(-5*time.Minute),NotAfter:now.AddDate(2,0,0),KeyUsage:x509.KeyUsageDigitalSignature|x509.KeyUsageKeyEncipherment,ExtKeyUsage:[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},BasicConstraintsValid:true,DNSNames:[]string{"localhost",host},IPAddresses:[]net.IP{net.ParseIP("127.0.0.1"),net.ParseIP("::1")}}
	if ip:=net.ParseIP(cfg.Host);ip!=nil{template.IPAddresses=append(template.IPAddresses,ip)}
	if interfaces, e := net.Interfaces(); e == nil {
		for _, iface := range interfaces {
			addrs, e := iface.Addrs(); if e != nil { continue }
			for _, addr := range addrs { if network, ok := addr.(*net.IPNet); ok && network.IP != nil { template.IPAddresses = append(template.IPAddresses, network.IP) } }
		}
	}
	der,err:=x509.CreateCertificate(rand.Reader,&template,&template,&key.PublicKey,key);if err!=nil{logger.Fatal(err)}
	certPEM:=pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:der});keyPEM:=pem.EncodeToMemory(&pem.Block{Type:"RSA PRIVATE KEY",Bytes:x509.MarshalPKCS1PrivateKey(key)})
	if err=os.WriteFile(certPath,certPEM,0600);err!=nil{logger.Fatal(err)}
	if err=os.WriteFile(keyPath,keyPEM,0600);err!=nil{logger.Fatal(err)}
	cfg.TLSCert=certPath;cfg.TLSKey=keyPath
	if err=saveConfig();err!=nil{logger.Fatal(err)}
	fmt.Printf("HTTPS certificate and key created. Config updated. Restart ezNAS to enable HTTPS.\nCertificate: %s\nPrivate key: %s\nNOTE: This is self-signed; browsers will show a trust warning until you trust the certificate or configure a trusted certificate.\n",certPath,keyPath)
}
