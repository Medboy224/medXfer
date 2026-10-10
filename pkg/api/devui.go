//go:build devui

package api

// The web dashboard is a development tool (DEV-19): it exists only in builds with the
// "devui" tag, together with the native file picker, the upload drop zone and the folder
// browser, which nothing else uses. Production binaries have none of it (CI checks).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// DashboardAvailable reports whether this build serves the web dashboard.
const DashboardAvailable = true

func (s *DaemonServer) registerDevUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/browse", s.control(s.handleBrowse))
	mux.HandleFunc("/api/upload", s.control(s.handleUpload))
	mux.HandleFunc("/api/fs/list", s.control(s.handleFSList))
	mux.HandleFunc("/api/fs/mkdir", s.control(s.handleFSMkdir))
}

// serveDashboard writes the dashboard page with this daemon's control token in it.
func (s *DaemonServer) serveDashboard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write([]byte(strings.Replace(IndexHTML, controlTokenPlaceholder, s.controlToken, 1)))
}

func (s *DaemonServer) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: Native file dialog is restricted to localhost", http.StatusForbidden)
		return
	}

	browseType := r.URL.Query().Get("type")
	w.Header().Set("Content-Type", "application/json")

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if browseType == "folder" {
			psScript := `Add-Type -AssemblyName System.Windows.Forms; $f = New-Object System.Windows.Forms.FolderBrowserDialog; $f.Description = 'Select a folder to send'; if($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK){ Write-Output $f.SelectedPath }`
			cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
		} else {
			psScript := `Add-Type -AssemblyName System.Windows.Forms; $f = New-Object System.Windows.Forms.OpenFileDialog; $f.Title = 'Select files to send'; $f.Multiselect = $true; if($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK){ Write-Output ($f.FileNames -join "` + "`" + `n") }`
			cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
		}
	case "darwin":
		if browseType == "folder" {
			cmd = exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "Select a folder to send")`)
		} else {
			cmd = exec.Command("osascript", "-e", `set f to choose file with prompt "Select files to send" with multiple selections allowed
set p to ""
repeat with i in f
    set p to p & (POSIX path of i) & linefeed
end repeat
return p`)
		}
	case "linux":
		if browseType == "folder" {
			if _, err := exec.LookPath("zenity"); err == nil {
				cmd = exec.Command("zenity", "--file-selection", "--directory", "--title=Select a folder to send")
			} else if _, err := exec.LookPath("kdialog"); err == nil {
				cmd = exec.Command("kdialog", "--getexistingdirectory")
			}
		} else {
			if _, err := exec.LookPath("zenity"); err == nil {
				cmd = exec.Command("zenity", "--file-selection", "--multiple", "--separator=\n", "--title=Select files to send")
			} else if _, err := exec.LookPath("kdialog"); err == nil {
				cmd = exec.Command("kdialog", "--getopenfilename", "--multiple", "--separate-output")
			}
		}
	}

	var paths []string
	if cmd != nil {
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					paths = append(paths, line)
				}
			}
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"paths": paths,
	})
}

func (s *DaemonServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: Staging upload is restricted to localhost", http.StatusForbidden)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	stageID := fmt.Sprintf("stage_%d", time.Now().UnixNano())
	stageDir := filepath.Join(os.TempDir(), "medxfer_staged", stageID)
	_ = os.MkdirAll(stageDir, 0755)

	var savedPaths []string

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		fileName := part.FileName()
		if fileName == "" {
			fileName = part.FormName()
		}
		if fileName == "" {
			continue
		}

		cleanRel := strings.ReplaceAll(fileName, "\\", "/")
		cleanRel = strings.TrimPrefix(cleanRel, "/")
		fullDest := filepath.Join(stageDir, filepath.FromSlash(cleanRel))

		_ = os.MkdirAll(filepath.Dir(fullDest), 0755)
		outFile, err := os.Create(fullDest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_, copyErr := io.Copy(outFile, part)
		outFile.Close()
		if copyErr != nil {
			http.Error(w, copyErr.Error(), http.StatusInternalServerError)
			return
		}

		savedPaths = append(savedPaths, fullDest)
	}

	w.Header().Set("Content-Type", "application/json")
	if len(savedPaths) > 0 {
		pathsToSend := savedPaths
		if len(savedPaths) > 1 {
			// Check if all files share a single common top-level folder inside stageDir
			// (e.g. when user selected a folder using webkitdirectory)
			firstRel, _ := filepath.Rel(stageDir, savedPaths[0])
			firstParts := strings.Split(filepath.ToSlash(firstRel), "/")
			if len(firstParts) > 1 {
				commonFolder := firstParts[0]
				allShare := true
				for _, sp := range savedPaths[1:] {
					rel, _ := filepath.Rel(stageDir, sp)
					parts := strings.Split(filepath.ToSlash(rel), "/")
					if len(parts) <= 1 || parts[0] != commonFolder {
						allShare = false
						break
					}
				}
				if allShare {
					// Send user's actual folder, so receiver creates user's folder, NOT stage_...
					pathsToSend = []string{filepath.Join(stageDir, commonFolder)}
				}
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "ok",
			"stage_dir":   stageDir,
			"paths":       pathsToSend,
			"total_files": len(savedPaths),
		})
	} else {
		http.Error(w, "no files received", http.StatusBadRequest)
	}
}

type FSQuickDir struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type FSDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type FSFileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type FSListResponse struct {
	CurrentDir string        `json:"current_dir"`
	ParentDir  string        `json:"parent_dir"`
	QuickDirs  []FSQuickDir  `json:"quick_dirs"`
	Dirs       []FSDirEntry  `json:"dirs"`
	Files      []FSFileEntry `json:"files"`
}

func isSystemMount(p string) bool {
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "/" {
		return false
	}
	systemPrefixes := []string{
		"/apex", "/bootstrap-apex", "/data", "/dev", "/proc", "/sys",
		"/system", "/vendor", "/product", "/omr", "/efs", "/cache",
		"/etc", "/bin", "/sbin", "/lib", "/lib64", "/usr", "/var", "/tmp",
		"/run/user", "/run/lock", "/run/systemd", "/snap",
		"/mnt/androidwritable", "/mnt/appfuse", "/mnt/asec", "/mnt/installer",
		"/mnt/knox", "/mnt/obb", "/mnt/runtime", "/mnt/secure", "/mnt/shell", "/mnt/user",
	}
	for _, sp := range systemPrefixes {
		if p == sp || strings.HasPrefix(p, sp+"/") {
			return true
		}
	}
	return false
}

func getLinuxMounts() []FSQuickDir {
	var mounts []FSQuickDir
	seenPaths := make(map[string]bool)

	addMount := func(name, path string) {
		path = filepath.Clean(path)
		if seenPaths[path] || isSystemMount(path) {
			return
		}
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			seenPaths[path] = true
			mounts = append(mounts, FSQuickDir{Name: name, Path: path})
		}
	}

	// 1. Inspect /proc/mounts and /etc/mtab for active storage mounts
	mountFiles := []string{"/proc/mounts", "/etc/mtab"}
	for _, mf := range mountFiles {
		data, err := os.ReadFile(mf)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 3 {
				continue
			}
			device := fields[0]
			mountPoint := fields[1]
			fstype := fields[2]

			if isSystemMount(mountPoint) {
				continue
			}

			isBlockDev := strings.HasPrefix(device, "/dev/sd") ||
				strings.HasPrefix(device, "/dev/nvme") ||
				strings.HasPrefix(device, "/dev/mmcblk") ||
				strings.HasPrefix(device, "/dev/vd") ||
				strings.HasPrefix(device, "/dev/mapper/") ||
				strings.HasPrefix(device, "/dev/disk/") ||
				strings.HasPrefix(device, "/dev/block/") ||
				device == "fuseblk" || strings.HasPrefix(fstype, "fuse")

			isMediaMount := strings.HasPrefix(mountPoint, "/media/") ||
				strings.HasPrefix(mountPoint, "/run/media/") ||
				strings.HasPrefix(mountPoint, "/mnt/") ||
				(strings.HasPrefix(mountPoint, "/storage/") && !strings.HasPrefix(mountPoint, "/storage/emulated"))

			isStorageFS := fstype == "ext4" || fstype == "ext3" || fstype == "ext2" ||
				fstype == "vfat" || fstype == "fat" || fstype == "exfat" ||
				fstype == "ntfs" || fstype == "ntfs3" || fstype == "fuseblk" ||
				fstype == "btrfs" || fstype == "xfs" || fstype == "f2fs" ||
				fstype == "iso9660" || fstype == "udf" || fstype == "cifs" || fstype == "nfs"

			if (isMediaMount || isBlockDev) && isStorageFS {
				baseName := filepath.Base(mountPoint)
				label := fmt.Sprintf("💾 %s", baseName)
				if strings.HasPrefix(mountPoint, "/media/") || strings.HasPrefix(mountPoint, "/run/media/") {
					label = fmt.Sprintf("🔌 %s", baseName)
				} else if strings.HasPrefix(mountPoint, "/mnt/") {
					label = fmt.Sprintf("💾 %s (mnt)", baseName)
				}
				addMount(label, mountPoint)
			}
		}
	}

	// 2. Direct directory scan of /media, /run/media, and /mnt
	scanDir := func(baseDir string, icon string) {
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(baseDir, e.Name())
			if isSystemMount(p) {
				continue
			}

			// If it's a user directory (like /media/username or /run/media/username), inspect its subdirectories
			subEntries, subErr := os.ReadDir(p)
			if subErr == nil && len(subEntries) > 0 {
				hasSubDirs := false
				for _, se := range subEntries {
					if se.IsDir() && !strings.HasPrefix(se.Name(), ".") {
						hasSubDirs = true
						subPath := filepath.Join(p, se.Name())
						addMount(fmt.Sprintf("%s %s", icon, se.Name()), subPath)
					}
				}
				if hasSubDirs {
					continue
				}
			}

			addMount(fmt.Sprintf("%s %s", icon, e.Name()), p)
		}
	}

	scanDir("/media", "🔌")
	scanDir("/run/media", "🔌")
	if user := os.Getenv("USER"); user != "" {
		scanDir(filepath.Join("/media", user), "🔌")
		scanDir(filepath.Join("/run/media", user), "🔌")
	}
	scanDir("/mnt", "💾")

	// 3. Android external SD card or USB OTG storage (/storage/XXXX-XXXX)
	if entries, err := os.ReadDir("/storage"); err == nil {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() && name != "self" && name != "emulated" && !strings.HasPrefix(name, ".") {
				p := filepath.Join("/storage", name)
				addMount(fmt.Sprintf("💾 SD/USB: %s", name), p)
			}
		}
	}

	// 4. Always add Root (/) for easy navigation on Unix
	addMount("🖥️ Racine (/)", "/")

	return mounts
}

func getQuickDirs() []FSQuickDir {
	var quick []FSQuickDir
	home, _ := os.UserHomeDir()

	// 1. Android shared storage directories
	androidDirs := []struct {
		name string
		path string
	}{
		{"📥 Downloads", "/storage/emulated/0/Download"},
		{"📁 Documents", "/storage/emulated/0/Documents"},
		{"📷 DCIM", "/storage/emulated/0/DCIM"},
		{"🎬 Movies", "/storage/emulated/0/Movies"},
		{"🎵 Music", "/storage/emulated/0/Music"},
		{"📱 SD Card", "/sdcard"},
	}
	for _, ad := range androidDirs {
		if fi, err := os.Stat(ad.path); err == nil && fi.IsDir() {
			quick = append(quick, FSQuickDir{Name: ad.name, Path: ad.path})
		}
	}

	// 2. User Home / Downloads / Documents
	if home != "" {
		dl := filepath.Join(home, "Downloads")
		if fi, err := os.Stat(dl); err == nil && fi.IsDir() {
			quick = append(quick, FSQuickDir{Name: "📥 Downloads", Path: dl})
		}
		docs := filepath.Join(home, "Documents")
		if fi, err := os.Stat(docs); err == nil && fi.IsDir() {
			quick = append(quick, FSQuickDir{Name: "📁 Documents", Path: docs})
		}
		quick = append(quick, FSQuickDir{Name: "🏠 Home", Path: home})
	}

	// 3. Windows Drives
	if runtime.GOOS == "windows" {
		for _, drive := range "CDEFGHIJKLMNOPQRSTUVWXYZ" {
			dPath := string(drive) + ":\\"
			if fi, err := os.Stat(dPath); err == nil && fi.IsDir() {
				quick = append(quick, FSQuickDir{Name: "💾 " + string(drive) + ":", Path: dPath})
			}
		}
	}

	// 4. Linux & Android External Drives and Mounted Partitions
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		quick = append(quick, getLinuxMounts()...)
	}

	return quick
}

func (s *DaemonServer) handleFSList(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: Filesystem browsing is restricted to localhost", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	targetDir := r.URL.Query().Get("dir")

	if targetDir == "" {
		s.mu.RLock()
		targetDir = s.config.DownloadDir
		s.mu.RUnlock()
	}

	if targetDir == "" || targetDir == "." {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dl := filepath.Join(home, "Downloads")
			if fi, err := os.Stat(dl); err == nil && fi.IsDir() {
				targetDir = dl
			} else {
				targetDir = home
			}
		} else {
			targetDir, _ = os.Getwd()
		}
	}

	absDir, err := filepath.Abs(targetDir)
	if err == nil {
		targetDir = absDir
	}

	entries, err := os.ReadDir(targetDir)
	var dirs []FSDirEntry
	var files []FSFileEntry
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") && len(name) > 1 {
				continue
			}
			if e.IsDir() {
				dirs = append(dirs, FSDirEntry{
					Name: name,
					Path: filepath.Join(targetDir, name),
				})
			} else {
				fi, err := e.Info()
				size := int64(0)
				if err == nil {
					size = fi.Size()
				}
				files = append(files, FSFileEntry{
					Name: name,
					Path: filepath.Join(targetDir, name),
					Size: size,
				})
			}
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	parent := filepath.Dir(targetDir)
	if parent == targetDir {
		parent = ""
	}

	_ = json.NewEncoder(w).Encode(FSListResponse{
		CurrentDir: targetDir,
		ParentDir:  parent,
		QuickDirs:  getQuickDirs(),
		Dirs:       dirs,
		Files:      files,
	})
}

func (s *DaemonServer) handleFSMkdir(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Forbidden: Directory creation is restricted to localhost", http.StatusForbidden)
		return
	}

	if r.Method != http.MethodPost { // no side effect on GET (API-04, control 6)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	parentDir := r.URL.Query().Get("dir")
	folderName := r.URL.Query().Get("name")

	if parentDir == "" || folderName == "" {
		http.Error(w, "missing dir or name", http.StatusBadRequest)
		return
	}

	target := filepath.Join(parentDir, folderName)
	if err := os.MkdirAll(target, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"path":   target,
	})
}
