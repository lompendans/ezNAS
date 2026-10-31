package main

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/sha256"
	"crypto/hmac"
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
}

type FileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Directory bool `json:"directory"`
	Size int64 `json:"size"`
	Modified time.Time `json:"modified"`
}

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
		case "password":
			if len(args) > 1 && args[1] == "set" { setPassword(); return }
			fmt.Println("Usage: eznas password set"); os.Exit(2)
		case "start":
			// Start runs in the foreground. Use a service manager for background operation.
		default:
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0]); printHelp(); os.Exit(2)
		}
	}
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
		logger.Printf("web interface: http://%s", displayAddress(cfg.Host, cfg.Port))
		if cfg.PasswordHash == "" { logger.Printf("WARNING: password protection is disabled") }
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) { logger.Fatalf("server: %v", err) }
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
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
	fmt.Println("  eznas password set Enable or change optional password protection")
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
		w.Header().Set("Content-Security-Policy","default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w,r)
	})
}
func statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { methodNotAllowed(w); return }
	cfgMu.RLock(); c := cfg; cfgMu.RUnlock()
	hostname, _ := os.Hostname()
	address := hostname + ":" + strconv.Itoa(c.Port)
	writeJSON(w, 200, map[string]any{"name":"ezNAS","version":version,"passwordRequired":c.PasswordHash!="","storage":c.Storage,"host":c.Host,"port":c.Port,"hostname":hostname,"address":address,"os":runtime.GOOS})
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
	case "/api/search":
		if r.Method!=http.MethodGet { methodNotAllowed(w); return }; searchFiles(w,r)
	case "/api/storage":
		if r.Method!=http.MethodGet { methodNotAllowed(w); return }; storageInfo(w)
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
	for _,e:=range entries { info,err:=e.Info();if err!=nil{continue}; rel,_:=filepath.Rel(cfg.Storage,filepath.Join(p,e.Name())); out=append(out,FileInfo{Name:e.Name(),Path:filepath.ToSlash(rel),Directory:e.IsDir(),Size:info.Size(),Modified:info.ModTime()}) }
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
	if err=os.RemoveAll(p);err!=nil{writeJSON(w,400,map[string]string{"error":err.Error()});return};writeJSON(w,200,map[string]bool{"ok":true})
}
func searchFiles(w http.ResponseWriter,r *http.Request) {
	q:=strings.TrimSpace(r.URL.Query().Get("q"));if q==""{writeJSON(w,200,map[string]any{"items":[]FileInfo{}});return}
	if len(q)>200{writeJSON(w,400,map[string]string{"error":"query too long"});return}
	cfgMu.RLock();root:=cfg.Storage;cfgMu.RUnlock();out:=[]FileInfo{};limit:=500
	err:=filepath.WalkDir(root,func(p string,d fs.DirEntry,err error)error{
		if err!=nil{return nil};if p==root{return nil}
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
