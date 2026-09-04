package api

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleSharePortal serves the mobile zero-install download & upload web page
func (s *DaemonServer) handleSharePortal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(SharePortalHTML))
}

// handleShareList returns the list of currently shared files as JSON
func (s *DaemonServer) handleShareList(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	m := s.lastOfferedManifest
	singleFile := s.lastOfferedFile
	devName := s.config.DeviceName
	s.mu.RUnlock()

	type SharedItem struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"url"`
	}

	var items []SharedItem
	var totalBytes int64
	if m != nil {
		totalBytes = m.TotalBytes
		for _, it := range m.Items {
			items = append(items, SharedItem{
				Name: it.RelPath,
				Size: it.Size,
				URL:  "/api/share/download?file=" + it.RelPath,
			})
		}
	} else if singleFile != "" {
		fi, err := os.Stat(singleFile)
		if err == nil {
			totalBytes = fi.Size()
			items = append(items, SharedItem{
				Name: filepath.Base(singleFile),
				Size: fi.Size(),
				URL:  "/api/share/download?file=" + filepath.Base(singleFile),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"device_name": devName,
		"items":       items,
		"total_count": len(items),
		"total_bytes": totalBytes,
	})
}

// progressResponseWriter intercepts writes to emit live WebSocket download telemetry
type progressResponseWriter struct {
	http.ResponseWriter
	server      *DaemonServer
	clientIP    string
	fileName    string
	totalBytes  int64
	transferred int64
	startTime   time.Time
	lastUpdate  time.Time
}

func (pw *progressResponseWriter) Write(p []byte) (int, error) {
	n, err := pw.ResponseWriter.Write(p)
	pw.transferred += int64(n)
	now := time.Now()
	if now.Sub(pw.lastUpdate) >= 300*time.Millisecond || (pw.totalBytes > 0 && pw.transferred >= pw.totalBytes) {
		pw.lastUpdate = now
		elapsed := now.Sub(pw.startTime).Seconds()
		var speed float64
		if elapsed > 0 {
			speed = float64(pw.transferred) / (1024 * 1024) / elapsed
		}
		pct := 0
		if pw.totalBytes > 0 {
			pct = int((pw.transferred * 100) / pw.totalBytes)
			if pct > 100 {
				pct = 100
			}
		}
		pw.server.Broadcast(NewEvent("web_share_progress", map[string]interface{}{
			"direction":   "download",
			"client_ip":   pw.clientIP,
			"file":        pw.fileName,
			"bytes":       pw.transferred,
			"total_bytes": pw.totalBytes,
			"percent":     pct,
			"speed_mbps":  fmt.Sprintf("%.1f", speed),
		}))
	}
	return n, err
}

// handleShareDownload streams the requested file or full zip on-the-fly with progress
func (s *DaemonServer) handleShareDownload(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Query().Get("file")
	allZip := r.URL.Query().Get("zip") == "true"

	clientIP := r.RemoteAddr
	if host, _, err := netSplitHost(clientIP); err == nil {
		clientIP = host
	}

	s.mu.RLock()
	m := s.lastOfferedManifest
	singleFile := s.lastOfferedFile
	s.mu.RUnlock()

	if allZip && m != nil {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", m.RootName))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", m.TotalBytes))

		pw := &progressResponseWriter{
			ResponseWriter: w,
			server:         s,
			clientIP:       clientIP,
			fileName:       m.RootName + ".zip",
			totalBytes:     m.TotalBytes,
			startTime:      time.Now(),
			lastUpdate:     time.Now(),
		}

		zw := zip.NewWriter(pw)
		for _, it := range m.Items {
			f, err := os.Open(it.FullPath)
			if err != nil {
				continue
			}
			zf, err := zw.Create(it.RelPath)
			if err == nil {
				_, _ = io.Copy(zf, f)
			}
			f.Close()
		}
		_ = zw.Close()

		s.Broadcast(NewEvent("web_share_complete", map[string]interface{}{
			"direction": "download",
			"client_ip": clientIP,
			"file":      m.RootName + ".zip",
		}))
		return
	}

	// Single file download
	var targetPath string
	if m != nil {
		for _, it := range m.Items {
			if it.RelPath == fileName || filepath.Base(it.RelPath) == fileName {
				targetPath = it.FullPath
				break
			}
		}
	} else if singleFile != "" && (filepath.Base(singleFile) == fileName || fileName == "") {
		targetPath = singleFile
	}

	if targetPath == "" {
		http.Error(w, "File not found or no files currently shared", http.StatusNotFound)
		return
	}

	f, err := os.Open(targetPath)
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	fi, _ := f.Stat()
	totalSize := fi.Size()
	cleanName := filepath.Base(targetPath)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", cleanName))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", totalSize))

	pw := &progressResponseWriter{
		ResponseWriter: w,
		server:         s,
		clientIP:       clientIP,
		fileName:       cleanName,
		totalBytes:     totalSize,
		startTime:      time.Now(),
		lastUpdate:     time.Now(),
	}

	_, _ = io.Copy(pw, f)

	s.Broadcast(NewEvent("web_share_complete", map[string]interface{}{
		"direction": "download",
		"client_ip": clientIP,
		"file":      cleanName,
	}))
}

func netSplitHost(remote string) (string, string, error) {
	idx := strings.LastIndex(remote, ":")
	if idx == -1 {
		return remote, "", nil
	}
	return remote[:idx], remote[idx+1:], nil
}

// handleShareUpload allows mobile browsers to upload photos and files to the PC with progress
func (s *DaemonServer) handleShareUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := netSplitHost(clientIP); err == nil {
		clientIP = host
	}

	contentLength := r.ContentLength

	s.Broadcast(NewEvent("web_share_progress", map[string]interface{}{
		"direction":   "upload",
		"client_ip":   clientIP,
		"file":        "Receiving mobile upload...",
		"bytes":       0,
		"total_bytes": contentLength,
		"percent":     0,
		"speed_mbps":  "0.0",
	}))

	err := r.ParseMultipartForm(512 * 1024 * 1024)
	if err != nil {
		http.Error(w, "Failed to parse multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	outDir := s.config.DownloadDir
	s.mu.RUnlock()

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	var savedNames []string
	for _, fileHeader := range files {
		src, err := fileHeader.Open()
		if err != nil {
			continue
		}

		cleanName := filepath.Base(fileHeader.Filename)
		destPath := filepath.Join(outDir, cleanName)

		dst, err := os.Create(destPath)
		if err != nil {
			src.Close()
			continue
		}

		_, _ = io.Copy(dst, src)
		src.Close()
		dst.Close()
		savedNames = append(savedNames, cleanName)
	}

	s.Broadcast(NewEvent("web_share_progress", map[string]interface{}{
		"direction":   "upload",
		"client_ip":   clientIP,
		"file":        fmt.Sprintf("%d files", len(savedNames)),
		"bytes":       contentLength,
		"total_bytes": contentLength,
		"percent":     100,
		"speed_mbps":  "0.0",
	}))

	s.Broadcast(NewEvent("web_share_complete", map[string]interface{}{
		"direction": "upload",
		"client_ip": clientIP,
		"files":     savedNames,
		"count":     len(savedNames),
	}))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"saved":   savedNames,
	})
}

const SharePortalHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
  <title>medXfer Mobile Share</title>
  <style>
    :root {
      --bg: #0f172a;
      --card: #1e293b;
      --border: #334155;
      --primary: #3b82f6;
      --primary-hover: #2563eb;
      --text: #f8fafc;
      --text-muted: #94a3b8;
      --success: #10b981;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); padding: 1.25rem; }
    .header { text-align: center; margin-bottom: 1.5rem; }
    .header h1 { font-size: 1.4rem; font-weight: 700; color: #60a5fa; }
    .header p { font-size: 0.85rem; color: var(--text-muted); margin-top: 0.25rem; }
    .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 1.25rem; margin-bottom: 1.25rem; }
    .card-title { font-size: 1rem; font-weight: 600; margin-bottom: 0.75rem; display: flex; justify-content: space-between; align-items: center; }
    .btn { display: inline-block; width: 100%; padding: 0.75rem; background: var(--primary); color: white; text-align: center; border-radius: 8px; font-weight: 600; font-size: 0.95rem; text-decoration: none; border: none; cursor: pointer; transition: background 0.2s; }
    .btn:active { background: var(--primary-hover); }
    .btn-success { background: var(--success); }
    .file-list { list-style: none; max-height: 280px; overflow-y: auto; margin-bottom: 1rem; }
    .file-item { display: flex; justify-content: space-between; align-items: center; padding: 0.6rem 0; border-bottom: 1px solid var(--border); font-size: 0.85rem; }
    .file-item:last-child { border-bottom: none; }
    .file-name { font-weight: 500; word-break: break-all; padding-right: 0.5rem; }
    .file-size { color: var(--text-muted); font-size: 0.75rem; white-space: nowrap; }
    .file-dl-btn { background: #334155; color: #60a5fa; padding: 0.4rem 0.75rem; border-radius: 6px; font-size: 0.8rem; text-decoration: none; font-weight: 600; border: none; cursor: pointer; }
    .file-dl-btn:active { background: #2563eb; color: white; }
    .upload-zone { border: 2px dashed var(--border); border-radius: 8px; padding: 1.25rem; text-align: center; cursor: pointer; }
    .upload-zone input { display: none; }
    .upload-text { font-size: 0.85rem; color: var(--text-muted); }
  </style>
</head>
<body>
  <div class="header">
    <h1>⚡ medXfer Mobile Share</h1>
    <p id="hostDeviceName">Connecting to host...</p>
  </div>

  <!-- Real-Time Download Progress Card -->
  <div class="card" id="dlProgressCard" style="display: none; border-color: #3b82f6; background: #0b1120;">
    <div class="card-title">
      <span id="dlFileName" style="font-size: 0.9rem; color: #60a5fa;">Downloading file...</span>
      <span id="dlSpeed" style="font-size: 0.85rem; color: #34d399; font-weight: 600;">0.0 MB/s</span>
    </div>
    <div style="background: #1e293b; border-radius: 6px; height: 10px; overflow: hidden; margin-bottom: 6px;">
      <div id="dlProgressBar" style="width: 0%; height: 100%; background: #3b82f6; transition: width 0.15s;"></div>
    </div>
    <div style="display: flex; justify-content: space-between; font-size: 0.75rem; color: var(--text-muted);">
      <span id="dlBytes">0 MB / 0 MB</span>
      <span id="dlPercent" style="font-weight: 600; color: #60a5fa;">0%</span>
    </div>
  </div>

  <!-- Real-Time Upload Progress Card -->
  <div class="card" id="ulProgressCard" style="display: none; border-color: #10b981; background: #0b1120;">
    <div class="card-title">
      <span id="ulFileName" style="font-size: 0.9rem; color: #34d399;">Uploading to PC...</span>
      <span id="ulSpeed" style="font-size: 0.85rem; color: #34d399; font-weight: 600;">0.0 MB/s</span>
    </div>
    <div style="background: #1e293b; border-radius: 6px; height: 10px; overflow: hidden; margin-bottom: 6px;">
      <div id="ulProgressBar" style="width: 0%; height: 100%; background: #10b981; transition: width 0.15s;"></div>
    </div>
    <div style="display: flex; justify-content: space-between; font-size: 0.75rem; color: var(--text-muted);">
      <span id="ulBytes">0 MB / 0 MB</span>
      <span id="ulPercent" style="font-weight: 600; color: #34d399;">0%</span>
    </div>
  </div>

  <!-- Shared Files from PC -->
  <div class="card">
    <div class="card-title">
      <span>Files from PC</span>
      <span id="fileCountBadge" style="font-size: 0.75rem; background: #334155; padding: 2px 8px; border-radius: 12px;">0 files</span>
    </div>
    <ul class="file-list" id="fileList">
      <li style="text-align: center; color: var(--text-muted); padding: 1rem;">No files currently shared by PC.</li>
    </ul>
    <button class="btn" id="dlAllBtn" style="display: none;" onclick="downloadWithProgress('/api/share/download?zip=true', 'medXfer_Bundle.zip')">⬇️ Download All (.zip)</button>
  </div>

  <!-- Upload to PC -->
  <div class="card">
    <div class="card-title">Send to PC</div>
    <div class="upload-zone" onclick="document.getElementById('mobileUpload').click()">
      <input type="file" multiple id="mobileUpload" onchange="uploadFiles(this.files)">
      <div style="font-size: 1.5rem; margin-bottom: 0.25rem;">📤</div>
      <div class="upload-text">Tap to select Photos, Videos, or Files to send to PC</div>
    </div>
  </div>

  <script>
    function formatBytes(bytes) {
      if (!bytes || bytes <= 0) return '0 B';
      const k = 1024;
      const dm = 1;
      const sizes = ['B', 'KB', 'MB', 'GB'];
      const i = Math.floor(Math.log(bytes) / Math.log(k));
      return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
    }

    async function loadSharedFiles() {
      try {
        const res = await fetch('/api/share/list');
        const data = await res.json();
        document.getElementById('hostDeviceName').innerText = 'Hosted by ' + (data.device_name || 'PC');
        document.getElementById('fileCountBadge').innerText = (data.items ? data.items.length : 0) + ' files (' + formatBytes(data.total_bytes) + ')';

        const listEl = document.getElementById('fileList');
        if (data.items && data.items.length > 0) {
          listEl.innerHTML = '';
          data.items.forEach(it => {
            const li = document.createElement('li');
            li.className = 'file-item';
            const clean = it.name.split('/').pop().split('\\').pop();
            li.innerHTML = '<span class="file-name">' + clean + '</span><div style="display:flex;align-items:center;gap:8px;"><span class="file-size">' + formatBytes(it.size) + '</span><button class="file-dl-btn" onclick="downloadWithProgress(\'' + it.url + '\', \'' + clean + '\')">Get</button></div>';
            listEl.appendChild(li);
          });
          if (data.items.length > 1) {
            document.getElementById('dlAllBtn').style.display = 'block';
          }
        }
      } catch (err) {
        console.error(err);
      }
    }

    async function downloadWithProgress(url, filename) {
      const card = document.getElementById("dlProgressCard");
      const bar = document.getElementById("dlProgressBar");
      const bytesText = document.getElementById("dlBytes");
      const pctText = document.getElementById("dlPercent");
      const speedText = document.getElementById("dlSpeed");
      const nameText = document.getElementById("dlFileName");

      card.style.display = "block";
      nameText.innerText = "Downloading " + filename + "...";
      bar.style.width = "0%";
      pctText.innerText = "0%";
      bytesText.innerText = "0 MB";
      speedText.innerText = "0.0 MB/s";

      const startTime = Date.now();
      try {
        const res = await fetch(url);
        if (!res.ok) throw new Error("HTTP " + res.status);
        const total = parseInt(res.headers.get("Content-Length") || "0", 10);
        const reader = res.body.getReader();
        let received = 0;
        const chunks = [];

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          chunks.push(value);
          received += value.length;

          const elapsed = (Date.now() - startTime) / 1000;
          const speed = elapsed > 0 ? (received / (1024 * 1024) / elapsed).toFixed(1) : "0.0";
          speedText.innerText = speed + " MB/s";

          if (total > 0) {
            const pct = Math.min(100, Math.round((received / total) * 100));
            bar.style.width = pct + "%";
            pctText.innerText = pct + "%";
            bytesText.innerText = formatBytes(received) + " / " + formatBytes(total);
          } else {
            bytesText.innerText = formatBytes(received);
          }
        }

        const blob = new Blob(chunks);
        const blobUrl = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = blobUrl;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(blobUrl), 10000);

        bar.style.width = "100%";
        pctText.innerText = "✓ 100%";
        nameText.innerText = "✓ Saved " + filename + " to downloads!";
      } catch (err) {
        alert("Download error: " + err.message);
      }
    }

    function uploadFiles(files) {
      if (!files || files.length === 0) return;
      const card = document.getElementById("ulProgressCard");
      const bar = document.getElementById("ulProgressBar");
      const bytesText = document.getElementById("ulBytes");
      const pctText = document.getElementById("ulPercent");
      const speedText = document.getElementById("ulSpeed");
      const nameText = document.getElementById("ulFileName");

      card.style.display = "block";
      nameText.innerText = "Uploading " + files.length + " file(s) to PC...";
      bar.style.width = "0%";
      pctText.innerText = "0%";
      bytesText.innerText = "0 MB";
      speedText.innerText = "0.0 MB/s";

      const formData = new FormData();
      for (let i = 0; i < files.length; i++) {
        formData.append("files", files[i]);
      }

      const xhr = new XMLHttpRequest();
      const startTime = Date.now();

      xhr.upload.onprogress = function(e) {
        if (e.lengthComputable) {
          const pct = Math.min(100, Math.round((e.loaded / e.total) * 100));
          bar.style.width = pct + "%";
          pctText.innerText = pct + "%";
          bytesText.innerText = formatBytes(e.loaded) + " / " + formatBytes(e.total);
          const elapsed = (Date.now() - startTime) / 1000;
          const speed = elapsed > 0 ? (e.loaded / (1024 * 1024) / elapsed).toFixed(1) : "0.0";
          speedText.innerText = speed + " MB/s";
        }
      };

      xhr.onload = function() {
        if (xhr.status === 200) {
          bar.style.width = "100%";
          pctText.innerText = "✓ 100%";
          nameText.innerText = "✓ Successfully sent " + files.length + " file(s) to PC!";
        } else {
          alert("Upload failed: " + xhr.statusText);
        }
      };

      xhr.onerror = function() {
        alert("Upload network error");
      };

      xhr.open("POST", "/api/share/upload");
      xhr.send(formData);
    }

    loadSharedFiles();
  </script>
</body>
</html>
`