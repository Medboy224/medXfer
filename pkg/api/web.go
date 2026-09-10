package api

const IndexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>medXfer Web Dashboard</title>
  <style>
    :root {
      --bg: #0f172a;
      --card: #1e293b;
      --border: #334155;
      --primary: #3b82f6;
      --primary-hover: #2563eb;
      --success: #10b981;
      --danger: #ef4444;
      --warning: #f59e0b;
      --text: #f8fafc;
      --muted: #94a3b8;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); padding: 20px; line-height: 1.5; }
    .container { max-width: 1100px; margin: 0 auto; }
    header { display: flex; justify-content: space-between; align-items: center; padding-bottom: 20px; border-bottom: 1px solid var(--border); margin-bottom: 24px; }
    .logo { font-size: 24px; font-weight: 700; display: flex; align-items: center; gap: 10px; }
    .logo span { color: var(--primary); }
    .badge { padding: 4px 10px; border-radius: 9999px; font-size: 12px; font-weight: 600; text-transform: uppercase; }
    .badge-online { background: rgba(16, 185, 129, 0.2); color: var(--success); border: 1px solid var(--success); }
    .badge-offline { background: rgba(239, 68, 68, 0.2); color: var(--danger); border: 1px solid var(--danger); }
    .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; margin-bottom: 20px; }
    @media (max-width: 768px) { .grid { grid-template-columns: 1fr; } }
    .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 20px; }
    .card h2 { font-size: 18px; margin-bottom: 16px; color: var(--text); display: flex; justify-content: space-between; align-items: center; }
    .form-group { margin-bottom: 14px; }
    label { display: block; font-size: 13px; color: var(--muted); margin-bottom: 6px; font-weight: 500; }
    input, select, textarea { width: 100%; padding: 10px 12px; background: #0b1120; border: 1px solid var(--border); border-radius: 8px; color: var(--text); font-size: 14px; outline: none; }
    input:focus, select:focus, textarea:focus { border-color: var(--primary); }
    .btn { display: inline-flex; align-items: center; justify-content: center; padding: 10px 16px; font-size: 14px; font-weight: 600; border-radius: 8px; border: none; cursor: pointer; transition: 0.15s; background: var(--primary); color: white; width: 100%; }
    .btn:hover { background: var(--primary-hover); }
    .btn-danger { background: var(--danger); }
    .btn-danger:hover { background: #dc2626; }
    .btn-success { background: var(--success); }
    .btn-success:hover { background: #059669; }
    .btn-secondary { background: #334155; }
    .btn-secondary:hover { background: #475569; }
    .btn-group { display: flex; gap: 8px; margin-bottom: 10px; }
    .btn-group .btn { width: auto; flex: 1; padding: 8px 12px; font-size: 13px; }
    .peer-item { display: flex; justify-content: space-between; align-items: center; padding: 10px 12px; background: #0b1120; border: 1px solid var(--border); border-radius: 8px; margin-bottom: 8px; }
    .peer-info { font-size: 14px; }
    .peer-sub { font-size: 12px; color: var(--muted); }
    .peer-actions { display: flex; gap: 8px; }
    .peer-actions .btn { width: auto; padding: 6px 12px; font-size: 12px; }
    
    .progress-section { margin-bottom: 14px; }
    .progress-bar-container { width: 100%; height: 12px; background: #0b1120; border-radius: 9999px; overflow: hidden; margin: 8px 0; border: 1px solid var(--border); }
    .progress-bar { height: 100%; width: 0%; background: linear-gradient(90deg, #3b82f6, #60a5fa); transition: width 0.1s ease; }
    .progress-bar-batch { background: linear-gradient(90deg, #10b981, #34d399); }
    
    .stats-row { display: flex; justify-content: space-between; font-size: 13px; color: var(--muted); margin-top: 10px; padding-top: 8px; border-top: 1px solid var(--border); }
    .log-box { background: #050811; border: 1px solid var(--border); border-radius: 8px; height: 180px; overflow-y: auto; padding: 12px; font-family: monospace; font-size: 12px; color: #cbd5e1; }
    .log-entry { margin-bottom: 4px; }
    .log-time { color: var(--muted); }
    .modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.7); display: none; align-items: center; justify-content: center; z-index: 100; backdrop-filter: blur(4px); }
    .modal { background: var(--card); border: 1px solid var(--border); border-radius: 16px; max-width: 440px; width: 90%; padding: 24px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.5); text-align: center; }
    .modal h3 { font-size: 20px; margin-bottom: 8px; }
    .modal p { color: var(--muted); font-size: 14px; margin-bottom: 20px; }
    .modal-btns { display: flex; gap: 12px; }
    .dir-item { display: flex; align-items: center; gap: 8px; padding: 8px 10px; border-radius: 6px; cursor: pointer; font-size: 13px; color: var(--text); user-select: none; transition: background 0.1s; }
    .dir-item:hover { background: rgba(59, 130, 246, 0.15); color: var(--primary); }
    .quick-chip { background: #0b1120; border: 1px solid var(--border); color: var(--text); border-radius: 9999px; padding: 3px 10px; font-size: 12px; cursor: pointer; transition: 0.15s; }
    .quick-chip:hover { border-color: var(--primary); color: var(--primary); }
    
    .drop-zone { border: 2px dashed var(--border); border-radius: 12px; padding: 24px 16px; text-align: center; background: #090d16; transition: all 0.2s ease; cursor: pointer; }
    .drop-zone:hover, .drop-zone.dragover { border-color: var(--primary); background: #111a2e; }
    .drop-icon { font-size: 32px; margin-bottom: 6px; }
    .drop-title { font-size: 14px; font-weight: 600; color: #f1f5f9; margin-bottom: 4px; }
    .drop-subtitle { font-size: 12px; color: var(--muted); margin-bottom: 12px; }
    .drop-buttons { display: flex; justify-content: center; gap: 12px; }
    .btn-picker { background: #1e293b; color: #93c5fd; border: 1px solid #3b82f6; padding: 8px 18px; font-size: 13px; border-radius: 8px; font-weight: 600; cursor: pointer; transition: all 0.15s; }
    .btn-picker:hover { background: #2563eb; color: white; }
  </style>
</head>
<body>
  <div class="container">
    <header>
      <div class="logo">⚡ med<span>Xfer</span> <small id="headerDeviceName" style="font-size: 13px; color: var(--primary); font-weight: 600; margin-left: 8px; background: rgba(59, 130, 246, 0.15); padding: 3px 8px; border-radius: 6px; border: 1px solid rgba(59, 130, 246, 0.3);">Device: Loading...</small></div>
      <div id="connectionStatus" class="badge badge-offline">Connecting...</div>
    </header>

    <!-- Incoming Web Share Upload Banner (Always visible at top when request arrives) -->
    <div id="webUploadBanner" style="display: none; background: #1e3a8a; border: 2px solid #3b82f6; border-radius: 12px; padding: 14px 18px; margin-bottom: 20px; align-items: center; justify-content: space-between; gap: 16px; box-shadow: 0 4px 20px rgba(59, 130, 246, 0.4);">
      <div style="flex: 1; min-width: 0;">
        <div style="font-weight: 700; color: #93c5fd; font-size: 15px; margin-bottom: 2px;">📲 Incoming Web Share Upload Request</div>
        <div id="webUploadBannerText" style="font-size: 13px; color: #f8fafc; word-break: break-all;">A device wants to upload files.</div>
      </div>
      <div style="display: flex; gap: 10px; flex-shrink: 0;">
        <button type="button" class="btn btn-danger" style="width: auto; padding: 8px 16px; font-size: 13px;" onclick="respondWebUpload(false)">✕ Reject</button>
        <button type="button" class="btn btn-success" style="width: auto; padding: 8px 20px; font-size: 13px; font-weight: 700;" onclick="respondWebUpload(true)">✓ Accept & Save</button>
      </div>
    </div>

    <!-- Security Alert Banner (For unauthorized / invalid PIN attempts) -->
    <div id="webSecurityBanner" style="display: none; background: #7f1d1d; border: 2px solid #ef4444; border-radius: 12px; padding: 12px 18px; margin-bottom: 20px; align-items: center; justify-content: space-between; gap: 16px; box-shadow: 0 4px 20px rgba(239, 68, 68, 0.4);">
      <div style="flex: 1; min-width: 0;">
        <div style="font-weight: 700; color: #fca5a5; font-size: 14px; margin-bottom: 2px;">⚠️ Web Share Security Alert</div>
        <div id="webSecurityBannerText" style="font-size: 13px; color: #fef2f2; word-break: break-all;">Unauthorized connection attempt detected.</div>
      </div>
      <button type="button" class="btn btn-secondary" style="width: auto; padding: 6px 12px; font-size: 12px; background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.2);" onclick="dismissSecurityAlert()">✕ Dismiss</button>
    </div>

    <div class="grid">
      <!-- Device & Settings Card -->
      <div class="card">
        <h2>Settings & Status <span id="nodeStatus" class="badge" style="background:#334155;">IDLE</span></h2>
        <div class="form-group">
          <label>Device Name</label>
          <input type="text" id="deviceNameInput" onchange="saveSettings()">
        </div>
        <div class="form-group">
          <label>Download Directory</label>
          <div style="display: flex; gap: 8px;">
            <input type="text" id="downloadDirInput" onchange="saveSettings()" placeholder="e.g. C:\Downloads or ~/Downloads">
            <button class="btn btn-secondary" style="width: auto; white-space: nowrap; padding: 0 12px;" onclick="openDirPicker('downloadDirInput')">📁 Browse...</button>
          </div>
        </div>
        <div class="form-group">
          <label>Collision Policy</label>
          <select id="collisionPolicySelect" onchange="saveSettings()">
            <option value="auto_rename">Auto-Rename (file (1).ext)</option>
            <option value="overwrite">Overwrite existing</option>
            <option value="skip">Skip existing</option>
          </select>
        </div>
        <button id="saveSettingsBtn" class="btn btn-secondary" onclick="saveSettings()">Save Settings</button>

        <div style="margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--border);">
          <div id="pairedBox" style="display: none;">
            <div style="font-size: 14px; margin-bottom: 10px;">🟢 Paired with: <strong id="pairedIP"></strong></div>
            <button class="btn btn-danger" onclick="disconnectNode()">Disconnect</button>
          </div>
          <div id="unpairedBox">
            <label>Direct Connect / Pair IP</label>
            <div style="display: flex; gap: 8px;">
              <input type="text" id="pairIPInput" placeholder="192.168.1.50">
              <button id="pairBtn" class="btn" style="width: auto;" onclick="pairNode()">Pair</button>
            </div>
          </div>
        </div>

        <!-- Connected Network Web Share Card -->
        <div id="localPortalCard" style="margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--border);">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
            <div style="display: flex; align-items: center; gap: 8px;">
              <label style="margin: 0; font-weight: 600;">📲 Instant Web Share</label>
              <span id="webShareStatusBadge" class="badge badge-offline" style="font-size: 10px; padding: 2px 7px;">DISABLED</span>
            </div>
            <div style="display: flex; gap: 6px; align-items: center;">
              <button id="toggleWebShareBtn" type="button" class="btn btn-success" style="width: auto; padding: 3px 10px; font-size: 11px; font-weight: 700;" onclick="toggleWebShare(true)">▶️ Turn ON</button>
              <span id="webSharePINBadge" style="display: none; font-size: 11px; background: #3b82f6; padding: 2px 8px; border-radius: 10px; color: white; font-weight: 700;">PIN: ------</span>
              <button id="webShare6DBtn" type="button" class="btn btn-secondary" style="display: none; width: auto; padding: 2px 6px; font-size: 10px;" onclick="regenerateSharePIN(6)" title="Generate new 6-digit PIN">🔄 6D</button>
              <button id="webShare4DBtn" type="button" class="btn btn-secondary" style="display: none; width: auto; padding: 2px 6px; font-size: 10px;" onclick="regenerateSharePIN(4)" title="Generate new 4-digit PIN">🔄 4D</button>
            </div>
          </div>

          <!-- Web Share Inactive Box -->
          <div id="webShareInactiveBox" style="background: #0b1120; border: 1px solid var(--border); border-radius: 8px; padding: 12px; margin-top: 6px; text-align: center;">
            <div style="font-size: 12px; color: var(--muted); margin-bottom: 8px;">
              Web Share is currently <strong>turned off</strong>. Remote devices on your Wi-Fi cannot access or upload files.
            </div>
            <button type="button" class="btn btn-success" style="width: auto; display: inline-flex; padding: 6px 14px; font-size: 12px; font-weight: 700;" onclick="toggleWebShare(true)">⚡ Turn ON Web Share</button>
          </div>

          <!-- Web Share Active Box -->
          <div id="webShareActiveBox" style="display: none;">
            <div style="font-size: 11px; color: var(--muted); margin-bottom: 10px;">
              Other devices on the same Wi-Fi? Scan QR code or open link to download/upload:
            </div>
            <div style="display: flex; gap: 12px; align-items: center; background: #0b1120; border: 1px solid var(--border); border-radius: 8px; padding: 10px;">
              <img id="localPortalQRImg" style="width: 85px; height: 85px; border-radius: 6px; background: white; padding: 4px;" alt="QR Code">
              <div style="flex: 1; min-width: 0;">
                <div style="font-size: 12px; font-weight: 600; color: #60a5fa; word-break: break-all;" id="localPortalURLText">http://...</div>
                <div style="font-size: 10px; color: var(--muted); margin-top: 4px;">Zero-install: Chrome / Safari / Firefox</div>
                <div style="display: flex; gap: 10px; align-items: center; margin-top: 6px;">
                  <a id="localPortalLink" href="/share" target="_blank" style="font-size: 11px; color: #34d399; text-decoration: none;">🔗 Open Portal</a>
                  <button type="button" class="btn btn-danger" style="width: auto; padding: 2px 8px; font-size: 10px;" onclick="toggleWebShare(false)">⏹️ Turn OFF</button>
                </div>
              </div>
            </div>
            <div style="margin-top: 8px; display: flex; justify-content: space-between; align-items: center; font-size: 11px; color: var(--muted);">
              <label style="display: flex; align-items: center; gap: 6px; cursor: pointer; margin: 0;">
                <input type="checkbox" id="webShareAutoAcceptCheck" onchange="toggleWebShareAutoAccept(this.checked)" style="width: auto;"> Auto-accept incoming web uploads
              </label>
            </div>
          </div>
        </div>

        <!-- Offline Hotspot & Zero-Install Web Share Card -->
        <div style="margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--border);">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px;">
            <label style="margin: 0; font-weight: 600;">📡 Offline Direct AP Hotspot</label>
            <span id="hotspotBadge" style="font-size: 11px; background: #334155; padding: 2px 8px; border-radius: 10px;">OFFLINE</span>
          </div>

          <div id="hotspotInactiveBox">
            <div style="display: flex; gap: 8px; align-items: center; margin-bottom: 8px;">
              <select id="hotspotBandSelect" style="padding: 6px 10px; font-size: 12px; width: 45%;">
                <option value="5ghz">5 GHz (Preferred)</option>
                <option value="2.4ghz">2.4 GHz (Legacy)</option>
              </select>
              <button id="startHotspotBtn" class="btn btn-secondary" style="width: 55%; font-size: 12px; padding: 6px 10px;" onclick="toggleHotspot(true)">⚡ Start Hotspot</button>
            </div>
            <button class="btn btn-secondary" style="width: 100%; font-size: 11px; padding: 6px; margin-bottom: 8px;" onclick="sendCmd('open_hotspot_settings')">⚙️ Open Windows 5 GHz Mobile Hotspot Settings</button>
            <div style="font-size: 11px; color: var(--muted);">Direct P2P hotspot for phone-to-PC file transfer when no Wi-Fi router is available.</div>
          </div>

          <div id="hotspotActiveBox" style="display: none; background: #0b1120; border: 1px solid var(--border); border-radius: 8px; padding: 12px;">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
              <div>
                <div style="font-size: 13px; font-weight: 600; color: #60a5fa;" id="hotspotSSIDDisplay">SSID: medXfer-XXXX</div>
                <div style="font-size: 12px; color: var(--muted);" id="hotspotPassDisplay">Pass: XXXXXXXX</div>
              </div>
              <button class="btn btn-danger" style="width: auto; padding: 4px 10px; font-size: 12px;" onclick="toggleHotspot(false)">Stop Hotspot</button>
            </div>

            <!-- Dual QR Codes: Wi-Fi + Web Portal -->
            <div style="display: flex; gap: 12px; justify-content: center; margin-top: 10px; text-align: center;">
              <div>
                <img id="wifiQRCodeImg" style="width: 105px; height: 105px; border-radius: 6px; background: white; padding: 4px;" alt="Wi-Fi QR">
                <div style="font-size: 10px; color: var(--muted); margin-top: 4px;">1. Scan to Connect</div>
              </div>
              <div>
                <img id="portalQRCodeImg" style="width: 105px; height: 105px; border-radius: 6px; background: white; padding: 4px;" alt="Portal QR">
                <div style="font-size: 10px; color: var(--muted); margin-top: 4px;">2. Scan to Open Web</div>
              </div>
            </div>
            <div style="text-align: center; margin-top: 8px;">
              <a id="hotspotPortalLink" href="/share" target="_blank" style="font-size: 11px; color: #60a5fa; text-decoration: none;">🔗 Open Mobile Share Portal</a>
            </div>

            <!-- Warning Banner when locked to 2.4GHz by connected router -->
            <div id="hotspotWarningBanner" style="display: none; background: #451a03; border: 1px solid #d97706; border-radius: 6px; padding: 8px; font-size: 11px; color: #fbbf24; margin-top: 10px;"></div>
          </div>
        </div>
      </div>

      <!-- Network Discovery & Transfer Card -->
      <div class="card">
        <h2>Nearby Devices <button class="btn btn-secondary" style="width: auto; padding: 4px 10px; font-size: 12px;" onclick="scanNetwork()">🔄 Scan</button></h2>
        <div id="peersList" style="min-height: 90px; margin-bottom: 16px;">
          <div style="color: var(--muted); font-size: 13px; text-align: center; padding: 20px;">Click "Scan" to discover devices on your Wi-Fi network</div>
        </div>

        <h2 style="margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--border);">Send Files or Folder</h2>
        
        <!-- Hidden Universal Browser File Inputs -->
        <input type="file" id="browserFileInput" multiple style="display:none" onchange="onBrowserFilesSelected(this.files)">
        <input type="file" id="browserFolderInput" webkitdirectory directory multiple style="display:none" onchange="onBrowserFilesSelected(this.files)">

        <!-- Unified Modern Drop & Pick Zone -->
        <div id="dropZone" class="drop-zone" ondragover="onDragOver(event)" ondragleave="onDragLeave(event)" ondrop="onDrop(event)">
          <div class="drop-icon">📦</div>
          <div class="drop-title">Drag & drop files or folders here</div>
          <div class="drop-subtitle">or choose from your device:</div>
          <div class="drop-buttons" onclick="event.stopPropagation()">
            <button type="button" class="btn-picker" onclick="triggerPickFiles()">📄 Choose Files</button>
            <button type="button" class="btn-picker" onclick="triggerPickFolder()">📁 Choose Folder</button>
          </div>
        </div>

        <!-- Selected files preview box -->
        <div id="selectedFilesBox" style="display:none; background:#0b1120; border:1px solid var(--border); border-radius:8px; padding:12px; margin-top:12px;">
          <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:6px;">
            <strong id="selectedFilesSummary" style="font-size:13px; color:var(--primary);">0 files selected</strong>
            <button type="button" class="btn btn-danger" style="width:auto; padding:3px 10px; font-size:11px;" onclick="clearAllSelected()">✕ Clear / Cancel</button>
          </div>
          <!-- Real-time Staging Progress Bar -->
          <div id="stagingProgressBarContainer" style="display:none; width: 100%; height: 6px; background: #1e293b; border-radius: 9999px; overflow: hidden; margin: 6px 0;">
            <div id="stagingProgressBar" style="height: 100%; width: 0%; background: #3b82f6; transition: width 0.1s ease;"></div>
          </div>
          <div id="selectedFilesList" style="font-size:12px; color:var(--muted); max-height:100px; overflow-y:auto;"></div>
        </div>

        <!-- Advanced Manual Path Toggle -->
        <div style="margin-top: 10px; text-align: right;">
          <a href="javascript:void(0)" onclick="toggleManualPath()" id="togglePathLink" style="font-size: 11px; color: var(--muted); text-decoration: none;">✏️ Advanced: Enter local path manually</a>
        </div>
        <div id="manualPathGroup" class="form-group" style="display: none; margin-top: 8px;">
          <div style="display: flex; gap: 8px;">
            <textarea id="sendPathInput" rows="2" placeholder="e.g. C:\Users\name\Documents\file.zip" oninput="onManualPathInput()" style="flex: 1;"></textarea>
            <button type="button" class="btn btn-secondary" style="width: auto; white-space: nowrap; padding: 0 12px; font-size: 12px;" onclick="openDirPicker('sendPathInput')">📁 Browse PC...</button>
          </div>
        </div>

        <div style="display: flex; gap: 8px; margin-top: 14px;">
          <button id="sendTransferBtn" class="btn btn-success" style="flex: 1;" onclick="sendTransfer()">🚀 Send to Paired Device</button>
          <button id="shareWebBtn" class="btn" style="flex: 1; background: #0284c7;" onclick="shareToWebPortal()">🌐 Share to Web Portal</button>
        </div>

        <!-- Web Shared Files Status -->
        <div id="webSharedFilesSummary" style="margin-top: 10px; padding: 8px 12px; background: #111e38; border: 1px solid #1e3a8a; border-radius: 6px; font-size: 12px; color: #34d399; display: none;">
          📁 <strong>Web Portal Files:</strong> <span id="webSharedFilesCount">0 files</span>
        </div>
      </div>
    </div>

    <!-- Real-Time Mobile Web Share Telemetry Card -->
    <div class="card" id="webShareProgressCard" style="display: none; margin-bottom: 20px; border: 1px solid #3b82f6; background: #070d19;">
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px;">
        <h2 style="margin: 0; font-size: 15px; color: #60a5fa;" id="webShareProgressTitle">📱 Mobile Web Transfer Active</h2>
        <div style="display: flex; align-items: center; gap: 8px;">
          <span id="webShareSpeed" style="font-size: 13px; color: #34d399; font-weight: 600;">0.0 MB/s</span>
          <button id="webSharePauseBtn" class="btn btn-secondary" style="width: auto; padding: 3px 10px; font-size: 11px;" onclick="toggleWebSharePause()">⏸️ Pause</button>
          <button id="webShareCancelBtn" class="btn btn-danger" style="width: auto; padding: 3px 10px; font-size: 11px;" onclick="cancelWebShareTransfer()">⏹️ Stop</button>
        </div>
      </div>
      <div class="progress-bar-container" style="margin-bottom: 8px; height: 10px;">
        <div class="progress-bar" id="webShareProgressBar" style="width: 0%; height: 100%; background: #3b82f6; transition: width 0.15s;"></div>
      </div>
      <div style="display: flex; justify-content: space-between; font-size: 12px; color: var(--muted);">
        <span id="webShareBytes">0 MB / 0 MB</span>
        <span id="webSharePercent" style="font-weight: 600; color: #60a5fa;">0%</span>
      </div>
    </div>

    <!-- Active Transfer Telemetry Card -->
    <div class="card" id="transferCard" style="display: none; margin-bottom: 20px;">
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
        <h2 style="margin: 0;">Live Transfer Telemetry</h2>
        <div style="display: flex; gap: 8px;">
          <button id="pauseResumeBtn" class="btn btn-secondary" style="width: auto; padding: 4px 12px; font-size: 12px;" onclick="togglePauseResume()">⏸️ Pause</button>
          <button class="btn btn-danger" style="width: auto; padding: 4px 12px; font-size: 12px;" onclick="cancelTransfer()">⏹️ Cancel</button>
        </div>
      </div>
      
      <!-- Current File Progress -->
      <div class="progress-section">
        <div style="display: flex; justify-content: space-between; align-items: center; font-size: 14px; font-weight: 600;">
          <span id="transferFileName" style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 60%;">File: test.mp4</span>
          <div style="display: flex; gap: 6px; align-items: center;">
            <button id="pauseCurrentFileBtn" class="btn btn-secondary" style="display: none; width: auto; padding: 2px 8px; font-size: 11px; background: #d97706;" onclick="pauseCurrentFileAndNext()" title="Pause this file and continue to next">⏸️ Pause File</button>
            <button id="skipCurrentFileBtn" class="btn btn-secondary" style="display: none; width: auto; padding: 2px 8px; font-size: 11px; background: #334155;" onclick="skipCurrentFile()" title="Skip this specific file">✕ Skip File</button>
            <span id="progressPercent">0%</span>
          </div>
        </div>
        <div class="progress-bar-container">
          <div class="progress-bar" id="progressBar"></div>
        </div>
        <div style="font-size: 12px; color: var(--muted); display: flex; justify-content: space-between;">
          <span id="fileBytesSpan">0 MB / 0 MB</span>
          <span id="fileIndexSpan">File 1 of 1</span>
        </div>
      </div>

      <!-- Total Batch Progress (Only shown when sending multiple files/folders) -->
      <div class="progress-section" id="batchProgressBox" style="display: none; margin-top: 14px; padding-top: 12px; border-top: 1px dashed var(--border);">
        <div style="display: flex; justify-content: space-between; font-size: 14px; font-weight: 600; color: #34d399;">
          <span>📁 Total Folder / Batch Progress</span>
          <span id="batchPercentSpan">0%</span>
        </div>
        <div class="progress-bar-container">
          <div class="progress-bar progress-bar-batch" id="batchProgressBar"></div>
        </div>
        <div style="font-size: 12px; color: var(--muted); display: flex; justify-content: space-between;">
          <span id="batchBytesSpan">0 MB / 0 MB</span>
          <span id="batchRemainingSpan">Remaining: 0 MB</span>
        </div>
      </div>

      <!-- Batch File Queue (Only shown for multi-file transfers) -->
      <div id="batchFilesQueueBox" style="display: none; margin-top: 14px; padding-top: 10px; border-top: 1px dashed var(--border);">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
          <span style="font-size: 13px; font-weight: 600; color: var(--primary);">📋 Batch File Queue (<span id="batchQueueCountSpan">0</span> files)</span>
        </div>
        <div id="batchFilesQueueList" style="background: #0b1120; border: 1px solid var(--border); border-radius: 8px; max-height: 180px; overflow-y: auto; padding: 6px; font-size: 12px;"></div>
      </div>

      <div class="stats-row">
        <div>Speed: <strong id="transferSpeed" style="color: var(--text);">0 MB/s</strong></div>
        <div>ETA: <strong id="transferETA" style="color: var(--text);">0s</strong></div>
      </div>
    </div>

    <!-- Live Event Log -->
    <div class="card">
      <h2>Live WebSocket Log Stream <button class="btn btn-secondary" style="width: auto; padding: 4px 10px; font-size: 12px;" onclick="clearLog()">Clear</button></h2>
      <div class="log-box" id="logBox"></div>
    </div>
  </div>

  <!-- Incoming Offer Modal -->
  <div class="modal-overlay" id="offerModal">
    <div class="modal">
      <h3 id="modalTitle">Incoming Transfer</h3>
      <p id="modalDesc">Peer wants to send you a file.</p>
      
      <div style="text-align: left; margin-bottom: 16px;">
        <label style="font-size: 12px; margin-bottom: 4px; color: var(--muted);">Save Destination Directory:</label>
        <div style="display: flex; gap: 8px;">
          <input type="text" id="modalSaveDirInput" placeholder="Save directory">
          <button class="btn btn-secondary" style="width: auto; white-space: nowrap; padding: 0 10px; font-size: 12px;" onclick="openDirPicker('modalSaveDirInput')">📁 Browse</button>
        </div>
      </div>

      <div class="modal-btns">
        <button class="btn btn-secondary" onclick="respondOffer(false)">Reject</button>
        <button class="btn btn-success" onclick="respondOffer(true)">Accept & Download</button>
      </div>
    </div>
  </div>

  <!-- Web Share Upload Approval Modal -->
  <div class="modal-overlay" id="webUploadModal" style="display: none; z-index: 99999;">
    <div class="modal">
      <h3 style="color: #60a5fa;">📲 Web Share Upload Request</h3>
      <p id="webUploadDesc" style="margin-bottom: 14px;">A device wants to upload files to this device.</p>
      
      <div id="webUploadFilesList" style="background: #0b1120; border: 1px solid var(--border); border-radius: 8px; max-height: 140px; overflow-y: auto; padding: 8px; font-size: 12px; text-align: left; margin-bottom: 16px;"></div>

      <div class="modal-btns">
        <button type="button" class="btn btn-secondary" onclick="respondWebUpload(false)">✕ Reject</button>
        <button type="button" class="btn btn-success" onclick="respondWebUpload(true)">✓ Accept & Save</button>
      </div>
    </div>
  </div>

  <!-- Universal Directory Picker Modal (Android, iOS, Linux, Mac, Windows) -->
  <div class="modal-overlay" id="dirPickerModal" style="display:none;">
    <div class="modal" style="max-width: 520px; width: 92%; text-align: left; padding: 20px;">
      <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:12px;">
        <h3 id="fsModalTitle" style="font-size:17px; margin:0;">📁 Select Folder</h3>
        <button onclick="closeDirPicker()" style="background:none; border:none; color:var(--muted); font-size:18px; cursor:pointer;">✕</button>
      </div>

      <!-- Quick Jump Roots (Android storage, Home, Downloads, Drives) -->
      <div id="fsQuickDirs" style="display:flex; gap:6px; flex-wrap:wrap; margin-bottom:10px;"></div>

      <!-- Current Path Bar -->
      <div style="display:flex; gap:6px; margin-bottom:10px;">
        <button id="fsUpBtn" class="btn btn-secondary" style="width:auto; padding:6px 12px; font-size:12px;" onclick="navFSParent()" title="Up to parent directory">⬆ Up</button>
        <input type="text" id="fsCurrentPath" style="padding:6px 10px; font-size:13px;" placeholder="/path/to/folder" onkeydown="if(event.key==='Enter') loadFSDir(this.value)">
        <button class="btn btn-secondary" style="width:auto; padding:6px 10px; font-size:12px;" onclick="loadFSDir(document.getElementById('fsCurrentPath').value)">Go</button>
        <button class="btn btn-secondary" style="width:auto; padding:6px 10px; font-size:12px;" onclick="promptNewFolder()" title="Create New Folder">➕</button>
      </div>

      <!-- Scrollable Directories List -->
      <div id="fsDirsList" style="background:#0b1120; border:1px solid var(--border); border-radius:8px; height:220px; overflow-y:auto; padding:6px; margin-bottom:14px;"></div>

      <div class="modal-btns">
        <button class="btn btn-secondary" onclick="closeDirPicker()">Cancel</button>
        <button class="btn btn-success" onclick="confirmSelectedDir()">✓ Select This Folder</button>
      </div>
    </div>
  </div>

  <script>
    let ws = null;
    let isTransferPaused = false;
    let currentBatchFiles = [];
    let activeFileIndex = 0;
    var pendingWebUploadTicket = null;
    const logBox = document.getElementById("logBox");

    function log(evt, data) {
      const time = new Date().toLocaleTimeString();
      const div = document.createElement("div");
      div.className = "log-entry";
      div.innerHTML = "<span class='log-time'>[" + time + "]</span> <strong>" + evt + "</strong>: " + (typeof data === 'object' ? JSON.stringify(data) : data);
      logBox.appendChild(div);
      logBox.scrollTop = logBox.scrollHeight;
    }

    function clearLog() {
      logBox.innerHTML = "";
    }

    function connectWS() {
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      const url = proto + "//" + window.location.host + "/ws";
      ws = new WebSocket(url);

      ws.onopen = () => {
        document.getElementById("connectionStatus").className = "badge badge-online";
        document.getElementById("connectionStatus").innerText = "CONNECTED";
        log("SYSTEM", "WebSocket connected to " + url);
        sendCmd("hotspot_status");
      };

      ws.onclose = () => {
        document.getElementById("connectionStatus").className = "badge badge-offline";
        document.getElementById("connectionStatus").innerText = "DISCONNECTED";
        log("SYSTEM", "WebSocket disconnected. Reconnecting in 2s...");
        setTimeout(connectWS, 2000);
      };

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data);
          handleEvent(msg);
        } catch (e) {
          log("ERROR", "Failed parsing event: " + event.data);
        }
      };
    }

    function sendCmd(action, payload = null, id = null) {
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        alert("WebSocket is not connected.");
        return;
      }
      const msg = { action: action, payload: payload, id: id || ("req_" + Date.now()) };
      ws.send(JSON.stringify(msg));
      log("SEND", msg);
    }

    function handleEvent(msg) {
      log("RECV:" + msg.event, msg.data);

      switch (msg.event) {
        case "status":
          updateStatusUI(msg.data);
          break;
        case "peers_list":
          renderPeers(msg.data);
          break;
        case "paired":
          const pairBtn = document.getElementById("pairBtn");
          if (pairBtn) { pairBtn.innerText = "Pair"; pairBtn.disabled = false; }
          document.querySelectorAll(".peer-actions button").forEach(b => {
            if (b.innerText.indexOf("Pairing") !== -1) {
              b.innerText = "Pair";
              b.disabled = false;
            }
          });
          document.getElementById("pairedBox").style.display = "block";
          document.getElementById("unpairedBox").style.display = "none";
          const pLabel = (msg.data.device_name && msg.data.device_name !== "Connecting...")
            ? (msg.data.device_name + " (" + msg.data.ip + ")")
            : msg.data.ip;
          document.getElementById("pairedIP").innerText = pLabel;
          document.getElementById("nodeStatus").innerText = "PAIRED";
          document.getElementById("nodeStatus").style.background = "var(--primary)";
          break;
        case "reconnecting":
          document.getElementById("nodeStatus").innerText = "RECONNECTING";
          document.getElementById("nodeStatus").style.background = "#d97706";
          break;
        case "disconnected":
          document.getElementById("pairedBox").style.display = "none";
          document.getElementById("unpairedBox").style.display = "block";
          document.getElementById("nodeStatus").innerText = "IDLE";
          document.getElementById("nodeStatus").style.background = "#334155";
          document.getElementById("offerModal").style.display = "none";
          break;
        case "incoming_offer":
          if (msg.data && msg.data.items) {
            currentBatchFiles = msg.data.items;
            renderBatchQueue();
          }
          showOfferModal(msg.data);
          break;
        case "batch_offered":
        case "batch_accepted":
          if (msg.data && msg.data.items) {
            currentBatchFiles = msg.data.items;
            renderBatchQueue();
          }
          break;
        case "transfer_start":
          document.getElementById("offerModal").style.display = "none";
          document.getElementById("transferCard").style.display = "block";
          document.getElementById("transferFileName").innerText = "File: " + msg.data.current_file;
          document.getElementById("fileIndexSpan").innerText = "File " + msg.data.file_index + " of " + msg.data.total_files;
          activeFileIndex = msg.data.file_index - 1;
          setPausedUI(false);
          
          if (msg.data.total_files > 1) {
            document.getElementById("batchProgressBox").style.display = "block";
            document.getElementById("pauseCurrentFileBtn").style.display = "inline-block";
            document.getElementById("skipCurrentFileBtn").style.display = "inline-block";
          } else {
            document.getElementById("batchProgressBox").style.display = "none";
            document.getElementById("pauseCurrentFileBtn").style.display = "none";
            document.getElementById("skipCurrentFileBtn").style.display = "none";
          }

          if (currentBatchFiles && currentBatchFiles[activeFileIndex]) {
            currentBatchFiles[activeFileIndex].status = "transferring";
            renderBatchQueue();
          }
          break;
        case "transfer_progress":
          document.getElementById("offerModal").style.display = "none";
          document.getElementById("transferCard").style.display = "block";
          
          if (msg.data.is_paused) {
            setPausedUI(true);
          } else if (isTransferPaused) {
            setPausedUI(false);
          }

          // Current File Bar
          document.getElementById("progressBar").style.width = msg.data.file_percent.toFixed(1) + "%";
          document.getElementById("progressPercent").innerText = msg.data.file_percent.toFixed(1) + "%";
          document.getElementById("transferFileName").innerText = "File: " + msg.data.current_file;
          document.getElementById("fileIndexSpan").innerText = "File " + msg.data.file_index + " of " + msg.data.total_files;
          document.getElementById("fileBytesSpan").innerText = formatMB(msg.data.file_bytes) + " / " + formatMB(msg.data.file_total_bytes);

          // Batch Bar (Only if multiple files)
          if (msg.data.total_files > 1) {
            document.getElementById("batchProgressBox").style.display = "block";
            document.getElementById("pauseCurrentFileBtn").style.display = "inline-block";
            document.getElementById("skipCurrentFileBtn").style.display = "inline-block";
            document.getElementById("batchProgressBar").style.width = msg.data.batch_percent.toFixed(1) + "%";
            document.getElementById("batchPercentSpan").innerText = msg.data.batch_percent.toFixed(1) + "%";
            document.getElementById("batchBytesSpan").innerText = formatMB(msg.data.batch_bytes) + " / " + formatMB(msg.data.batch_total_bytes);
            const remaining = Math.max(0, msg.data.batch_total_bytes - msg.data.batch_bytes);
            document.getElementById("batchRemainingSpan").innerText = "Remaining: " + formatMB(remaining);
          } else {
            document.getElementById("batchProgressBox").style.display = "none";
            document.getElementById("pauseCurrentFileBtn").style.display = "none";
            document.getElementById("skipCurrentFileBtn").style.display = "none";
          }

          if (isTransferPaused) {
            document.getElementById("transferSpeed").innerText = "0.0 MB/s (Paused)";
            document.getElementById("transferETA").innerText = "Paused";
          } else {
            document.getElementById("transferSpeed").innerText = msg.data.speed_mbps.toFixed(1) + " MB/s";
            document.getElementById("transferETA").innerText = formatETA(msg.data.eta_seconds);
          }
          break;
        case "item_completed":
        case "file_complete":
          if (msg.data && msg.data.items) {
            currentBatchFiles = msg.data.items;
          } else if (msg.data && msg.data.item_index !== undefined && currentBatchFiles && currentBatchFiles[msg.data.item_index]) {
            currentBatchFiles[msg.data.item_index].status = "completed";
          } else if (currentBatchFiles && currentBatchFiles[activeFileIndex]) {
            currentBatchFiles[activeFileIndex].status = "completed";
          }
          renderBatchQueue();
          break;
        case "batch_item_transferring":
          if (msg.data && msg.data.items) {
            currentBatchFiles = msg.data.items;
          } else if (msg.data && msg.data.item_index !== undefined && currentBatchFiles && currentBatchFiles[msg.data.item_index]) {
            currentBatchFiles[msg.data.item_index].status = "transferring";
          }
          renderBatchQueue();
          break;
        case "file_skipped":
          if (msg.data && msg.data.items) {
            currentBatchFiles = msg.data.items;
          } else if (currentBatchFiles && currentBatchFiles[msg.data.item_index]) {
            currentBatchFiles[msg.data.item_index].status = "skipped";
          }
          renderBatchQueue();
          break;
        case "file_paused":
          if (msg.data && msg.data.items) {
            currentBatchFiles = msg.data.items;
          } else if (currentBatchFiles && currentBatchFiles[msg.data.item_index]) {
            currentBatchFiles[msg.data.item_index].status = "paused";
          }
          renderBatchQueue();
          break;
        case "file_resumed":
          if (msg.data && msg.data.items) {
            currentBatchFiles = msg.data.items;
          } else if (currentBatchFiles && currentBatchFiles[msg.data.item_index]) {
            currentBatchFiles[msg.data.item_index].status = "pending";
          }
          renderBatchQueue();
          break;
        case "batch_paused_waiting":
          if (msg.data && msg.data.items) {
            currentBatchFiles = msg.data.items;
            renderBatchQueue();
          }
          document.getElementById("transferSpeed").innerText = "0.0 MB/s (Queue Paused)";
          document.getElementById("transferETA").innerText = "Waiting for resume";
          break;
        case "transfer_paused":
          setPausedUI(true);
          break;
        case "transfer_resumed":
          setPausedUI(false);
          break;
        case "transfer_complete":
        case "transfer_canceled":
        case "transfer_rejected":
          document.getElementById("offerModal").style.display = "none";
          document.getElementById("transferCard").style.display = "none";
          document.getElementById("pauseCurrentFileBtn").style.display = "none";
          document.getElementById("skipCurrentFileBtn").style.display = "none";
          document.getElementById("batchFilesQueueBox").style.display = "none";
          currentBatchFiles = [];
          setPausedUI(false);
          document.getElementById("nodeStatus").innerText = document.getElementById("pairedBox").style.display === "block" ? "PAIRED" : "IDLE";
          document.getElementById("nodeStatus").style.background = document.getElementById("pairedBox").style.display === "block" ? "var(--primary)" : "#334155";

          // Clear and reset progress bars and clear staged/selected files from UI
          const fpBar = document.getElementById("progressBar");
          if (fpBar) fpBar.style.width = "0%";
          const bpBar = document.getElementById("batchProgressBar");
          if (bpBar) bpBar.style.width = "0%";
          const fpText = document.getElementById("progressPercent");
          if (fpText) fpText.innerText = "0%";
          const bpText = document.getElementById("batchPercentSpan");
          if (bpText) bpText.innerText = "0%";
          const spText = document.getElementById("transferSpeed");
          if (spText) spText.innerText = "0.0 MB/s";
          clearAllSelected(true);
          break;
        case "action_error":
          const errPairBtn = document.getElementById("pairBtn");
          if (errPairBtn) { errPairBtn.innerText = "Pair"; errPairBtn.disabled = false; }
          document.querySelectorAll(".peer-actions button").forEach(b => {
            if (b.innerText.indexOf("Pairing") !== -1) {
              b.innerText = "Pair";
              b.disabled = false;
            }
          });
          isSendingTransfer = false;
          isSharingWeb = false;
          const sBtn = document.getElementById("sendTransferBtn");
          if (sBtn) { sBtn.innerText = "🚀 Send to Paired Device"; sBtn.disabled = false; }
          const wBtn = document.getElementById("shareWebBtn");
          if (wBtn) { wBtn.innerText = "🌐 Share to Web Portal"; wBtn.disabled = false; }
          const errMsg = msg.data && msg.data.error ? msg.data.error : "Unknown error";
          alert("Transfer Error: " + errMsg);
          break;
        case "hotspot_started":
        case "hotspot_status":
          const startBtn = document.getElementById("startHotspotBtn");
          if (startBtn) { startBtn.innerText = "⚡ Start Hotspot"; startBtn.disabled = false; }
          if (msg.data && msg.data.active) {
            document.getElementById("hotspotBadge").innerText = "ONLINE (" + (msg.data.band || "5 GHz") + ")";
            document.getElementById("hotspotBadge").style.background = "var(--success)";
            document.getElementById("hotspotInactiveBox").style.display = "none";
            document.getElementById("hotspotActiveBox").style.display = "block";
            document.getElementById("hotspotSSIDDisplay").innerText = "SSID: " + msg.data.ssid;
            document.getElementById("hotspotPassDisplay").innerText = "Pass: " + msg.data.password;
            if (msg.data.qr_wifi) document.getElementById("wifiQRCodeImg").src = msg.data.qr_wifi;
            if (msg.data.qr_portal) document.getElementById("portalQRCodeImg").src = msg.data.qr_portal;
            if (msg.data.portal_url) document.getElementById("hotspotPortalLink").href = msg.data.portal_url;

            if (msg.data.warning) {
              document.getElementById("hotspotWarningBanner").style.display = "block";
              document.getElementById("hotspotWarningBanner").innerText = "⚠️ " + msg.data.warning;
            } else {
              document.getElementById("hotspotWarningBanner").style.display = "none";
            }
          } else {
            document.getElementById("hotspotBadge").innerText = "OFFLINE";
            document.getElementById("hotspotBadge").style.background = "#334155";
            document.getElementById("hotspotInactiveBox").style.display = "block";
            document.getElementById("hotspotActiveBox").style.display = "none";
            document.getElementById("hotspotWarningBanner").style.display = "none";
          }
          break;
        case "hotspot_stopped":
          document.getElementById("hotspotBadge").innerText = "OFFLINE";
          document.getElementById("hotspotBadge").style.background = "#334155";
          document.getElementById("hotspotInactiveBox").style.display = "block";
          document.getElementById("hotspotActiveBox").style.display = "none";
          document.getElementById("hotspotWarningBanner").style.display = "none";
          break;
        case "web_files_shared":
          document.getElementById("webSharedFilesSummary").style.display = "block";
          document.getElementById("webSharedFilesCount").innerText = msg.data.count + " item(s) (" + formatMB(msg.data.total_bytes || msg.data.size) + ")";
          break;
        case "web_share_progress":
          const shareCard = document.getElementById("webShareProgressCard");
          if (shareCard) {
            shareCard.style.display = "block";
            const pTitle = msg.data.direction === "upload"
              ? "📥 Receiving upload from " + msg.data.client_ip
              : "📤 Sending " + msg.data.file + " to " + msg.data.client_ip;
            document.getElementById("webShareProgressTitle").innerText = pTitle;
            document.getElementById("webShareProgressBar").style.width = msg.data.percent + "%";
            document.getElementById("webShareProgressBar").style.background = "#3b82f6";
            document.getElementById("webSharePercent").innerText = msg.data.percent + "%";
            document.getElementById("webShareBytes").innerText = formatMB(msg.data.bytes) + " / " + formatMB(msg.data.total_bytes);
            const spdNum = parseFloat(msg.data.speed_mbps || "0");
            if (spdNum > 0) {
              document.getElementById("webShareSpeed").innerText = spdNum < 0.1 ? (spdNum * 1024).toFixed(0) + " KB/s" : spdNum.toFixed(1) + " MB/s";
            } else {
              document.getElementById("webShareSpeed").innerText = (msg.data.speed_mbps || "0.0") + " MB/s";
            }
          }
          break;
        case "web_share_complete":
          const cCard = document.getElementById("webShareProgressCard");
          if (cCard) {
            document.getElementById("webShareProgressBar").style.width = "100%";
            document.getElementById("webSharePercent").innerText = "100%";
            document.getElementById("webShareSpeed").innerText = "✓ Done";
            const cTitle = msg.data.direction === "upload"
              ? "✓ Upload from " + msg.data.client_ip + " saved to PC downloads!"
              : "✓ Finished sending " + (msg.data.file || "file") + " to " + msg.data.client_ip;
            document.getElementById("webShareProgressTitle").innerText = cTitle;
            setTimeout(() => {
              cCard.style.display = "none";
              document.getElementById("webShareProgressBar").style.width = "0%";
              document.getElementById("webSharePercent").innerText = "0%";
              document.getElementById("webShareSpeed").innerText = "0.0 MB/s";
              const pBtn = document.getElementById("webSharePauseBtn");
              if (pBtn) {
                pBtn.innerText = "⏸️ Pause";
                pBtn.className = "btn btn-secondary";
              }
            }, 2500);
          }
          break;
        case "web_share_paused":
          const pBtn = document.getElementById("webSharePauseBtn");
          if (pBtn) {
            pBtn.innerText = "▶️ Resume";
            pBtn.className = "btn btn-success";
          }
          const sTitle = document.getElementById("webShareProgressTitle");
          if (sTitle && !sTitle.innerText.includes("(Paused)")) {
            sTitle.innerText += " (Paused)";
          }
          document.getElementById("webShareSpeed").innerText = "⏸️ Paused";
          break;
        case "web_share_resumed":
          const rBtn = document.getElementById("webSharePauseBtn");
          if (rBtn) {
            rBtn.innerText = "⏸️ Pause";
            rBtn.className = "btn btn-secondary";
          }
          const resTitle = document.getElementById("webShareProgressTitle");
          if (resTitle) {
            resTitle.innerText = resTitle.innerText.replace(" (Paused)", "");
          }
          break;
        case "web_share_canceled":
          const cEl = document.getElementById("webShareProgressCard");
          if (cEl) {
            const who = msg.data && msg.data.client_ip ? " (" + msg.data.client_ip + ")" : "";
            document.getElementById("webShareProgressTitle").innerText = "⏹️ Web Share Transfer Canceled" + who;
            document.getElementById("webShareSpeed").innerText = "Canceled";
            document.getElementById("webShareProgressBar").style.background = "var(--danger)";
            setTimeout(() => {
              cEl.style.display = "none";
              document.getElementById("webShareProgressBar").style.width = "0%";
              document.getElementById("webShareProgressBar").style.background = "#3b82f6";
              document.getElementById("webSharePercent").innerText = "0%";
              document.getElementById("webShareSpeed").innerText = "0.0 MB/s";
              const pBtn = document.getElementById("webSharePauseBtn");
              if (pBtn) {
                pBtn.innerText = "⏸️ Pause";
                pBtn.className = "btn btn-secondary";
              }
            }, 1200);
          }
          break;
        case "web_files_cleared":
          const summaryEl = document.getElementById("webSharedFilesSummary");
          if (summaryEl) summaryEl.style.display = "none";
          const countEl = document.getElementById("webSharedFilesCount");
          if (countEl) countEl.innerText = "0 files";
          break;
        case "mobile_files_uploaded":
          console.log("Mobile files uploaded:", msg.data);
          break;
        case "web_share_upload_request":
          showWebUploadModal(msg.data);
          break;
        case "web_share_upload_accepted":
        case "web_share_upload_rejected":
          closeWebUploadModal();
          break;
        case "web_share_auth_failed":
          showSecurityAlert(msg.data);
          break;
        case "web_share_brute_force_blocked":
          showBruteForceAlert(msg.data);
          break;
      }
    }

    function showBruteForceAlert(data) {
      if (!data) return;
      const banner = document.getElementById("webSecurityBanner");
      const bannerText = document.getElementById("webSecurityBannerText");
      if (banner && bannerText) {
        const ip = data.client_ip || "Unknown IP";
        const fails = data.fail_count || 5;
        const sec = data.locked_seconds || 60;
        let msg = "🚨 BRUTE-FORCE ATTACK BLOCKED: IP " + ip + " failed " + fails + " times and is locked out for " + sec + "s.";
        if (data.pin_regenerated) {
          msg += " Web Share PIN was automatically rotated for security!";
        }
        bannerText.innerText = msg;
        banner.style.background = "#991b1b";
        banner.style.borderColor = "#f87171";
        banner.style.display = "flex";

        if (securityBannerTimeout) clearTimeout(securityBannerTimeout);
        securityBannerTimeout = setTimeout(() => {
          banner.style.display = "none";
          banner.style.background = "#7f1d1d";
          banner.style.borderColor = "#ef4444";
        }, 15000);
      }

      // Play emergency siren tone (3 rapid rising alarm tones)
      try {
        const audioCtx = new (window.AudioContext || window.webkitAudioContext)();
        for (let i = 0; i < 3; i++) {
          const osc = audioCtx.createOscillator();
          const gain = audioCtx.createGain();
          osc.type = "sawtooth";
          osc.frequency.setValueAtTime(440 + i * 120, audioCtx.currentTime + i * 0.15);
          gain.gain.setValueAtTime(0.25, audioCtx.currentTime + i * 0.15);
          gain.gain.exponentialRampToValueAtTime(0.01, audioCtx.currentTime + (i + 1) * 0.15);
          osc.connect(gain);
          gain.connect(audioCtx.destination);
          osc.start(audioCtx.currentTime + i * 0.15);
          osc.stop(audioCtx.currentTime + (i + 1) * 0.15);
        }
      } catch (e) {}
    }

    let securityBannerTimeout = null;
    function showSecurityAlert(data) {
      if (!data) return;
      const banner = document.getElementById("webSecurityBanner");
      const bannerText = document.getElementById("webSecurityBannerText");
      if (banner && bannerText) {
        const ip = data.client_ip || "Unknown IP";
        const t = data.timestamp || new Date().toLocaleTimeString();
        bannerText.innerText = "Device at " + ip + " attempted to unlock Web Share with an invalid PIN at " + t + ".";
        banner.style.display = "flex";

        if (securityBannerTimeout) clearTimeout(securityBannerTimeout);
        securityBannerTimeout = setTimeout(() => {
          banner.style.display = "none";
        }, 8000);
      }

      // Play alert tone (two quick warning beeps)
      try {
        const audioCtx = new (window.AudioContext || window.webkitAudioContext)();
        const osc = audioCtx.createOscillator();
        const gain = audioCtx.createGain();
        osc.type = "sawtooth";
        osc.frequency.setValueAtTime(300, audioCtx.currentTime);
        osc.frequency.setValueAtTime(200, audioCtx.currentTime + 0.1);
        gain.gain.setValueAtTime(0.15, audioCtx.currentTime);
        gain.gain.exponentialRampToValueAtTime(0.01, audioCtx.currentTime + 0.25);
        osc.connect(gain);
        gain.connect(audioCtx.destination);
        osc.start();
        osc.stop(audioCtx.currentTime + 0.25);
      } catch (e) {}
    }

    function dismissSecurityAlert() {
      if (securityBannerTimeout) clearTimeout(securityBannerTimeout);
      const banner = document.getElementById("webSecurityBanner");
      if (banner) banner.style.display = "none";
    }

    function showWebUploadModal(data) {
      if (!data) return;
      pendingWebUploadTicket = data.ticket;
      const count = data.file_count || (data.files ? data.files.length : 1);
      const totalBytesStr = data.total_bytes ? formatMB(data.total_bytes) : "";
      const desc = "Device " + (data.client_ip || "Peer") + " wants to send " + count + " file(s) " + (totalBytesStr ? "(" + totalBytesStr + ")" : "");

      // 1. Show interactive top banner
      const banner = document.getElementById("webUploadBanner");
      const bannerText = document.getElementById("webUploadBannerText");
      if (banner && bannerText) {
        bannerText.innerText = desc + ". Do you accept this upload?";
        banner.style.display = "flex";
      }

      // 2. Show modal overlay
      const modal = document.getElementById("webUploadModal");
      const descEl = document.getElementById("webUploadDesc");
      const listDiv = document.getElementById("webUploadFilesList");
      if (modal) {
        if (descEl) descEl.innerText = desc + ":";
        if (listDiv) {
          listDiv.innerHTML = "";
          if (data.files && data.files.length) {
            data.files.forEach(f => {
              const item = document.createElement("div");
              item.style.padding = "4px 0";
              item.style.borderBottom = "1px solid rgba(255,255,255,0.05)";
              item.innerText = "📄 " + f.name + (f.size ? " (" + formatMB(f.size) + ")" : "");
              listDiv.appendChild(item);
            });
          }
        }
        modal.style.display = "flex";
      }

      // 3. Audio chime if supported
      try {
        const audioCtx = new (window.AudioContext || window.webkitAudioContext)();
        const osc = audioCtx.createOscillator();
        osc.type = 'sine';
        osc.frequency.setValueAtTime(587.33, audioCtx.currentTime);
        osc.connect(audioCtx.destination);
        osc.start();
        osc.stop(audioCtx.currentTime + 0.15);
      } catch (e) {}
    }

    function closeWebUploadModal() {
      const modal = document.getElementById("webUploadModal");
      if (modal) modal.style.display = "none";
      const banner = document.getElementById("webUploadBanner");
      if (banner) banner.style.display = "none";
    }

    function respondWebUpload(accept) {
      closeWebUploadModal();
      if (!pendingWebUploadTicket) return;
      if (accept) {
        sendCmd("accept_web_upload", { ticket: pendingWebUploadTicket });
      } else {
        sendCmd("reject_web_upload", { ticket: pendingWebUploadTicket });
      }
      pendingWebUploadTicket = null;
    }

    function regenerateSharePIN(digits) {
      sendCmd("regenerate_share_pin", { digits: digits || 6 });
    }

    function toggleWebShare(enable) {
      sendCmd("toggle_web_share", { enabled: enable });
    }

    function toggleWebShareAutoAccept(checked) {
      sendCmd("set_web_share_auto_accept", { auto_accept: checked });
    }

    let isSharingWeb = false;
    async function shareToWebPortal() {
      if (isSharingWeb) return;
      isSharingWeb = true;
      const btn = document.getElementById("shareWebBtn");
      const origText = btn ? btn.innerText : "";
      if (btn) btn.innerText = "⏳ Sharing...";
      try {
        const paths = await preparePathsForSend();
        if (!paths || paths.length === 0) {
          alert("Please select files/folders or enter path(s) first!");
          return;
        }
        sendCmd("share_web_files", { paths: paths });
      } finally {
        setTimeout(() => {
          isSharingWeb = false;
          if (btn) btn.innerText = origText;
        }, 800);
      }
    }

    function toggleHotspot(start) {
      if (start) {
        const band = document.getElementById("hotspotBandSelect").value;
        const btn = document.getElementById("startHotspotBtn");
        if (btn) { btn.innerText = "Starting..."; btn.disabled = true; }
        sendCmd("hotspot_start", { band: band });
      } else {
        sendCmd("hotspot_stop");
      }
    }

    function formatMB(bytes) {
      if (!bytes || bytes <= 0) return "0 MB";
      if (bytes >= 1024 * 1024 * 1024) {
        return (bytes / (1024 * 1024 * 1024)).toFixed(2) + " GB";
      }
      return (bytes / (1024 * 1024)).toFixed(2) + " MB";
    }

    function formatETA(seconds) {
      if (!seconds || seconds <= 0) return "0s";
      if (seconds < 60) {
        return seconds + "s";
      }
      const mins = Math.floor(seconds / 60);
      const secs = seconds % 60;
      if (mins < 60) {
        return secs > 0 ? (mins + "m " + secs + "s") : (mins + "m");
      }
      const hours = Math.floor(mins / 60);
      const remMins = mins % 60;
      return remMins > 0 ? (hours + "h " + remMins + "m") : (hours + "h");
    }

    function updateStatusUI(st) {
      if (!st) return;
      if (st.device_name) {
        document.getElementById("headerDeviceName").innerText = "Device: " + st.device_name;
      }
      document.getElementById("deviceNameInput").value = st.device_name || "";
      document.getElementById("downloadDirInput").value = st.download_dir || "";
      document.getElementById("collisionPolicySelect").value = st.collision_policy || "auto_rename";

      if (st.paired) {
        document.getElementById("pairedBox").style.display = "block";
        document.getElementById("unpairedBox").style.display = "none";
        const label = (st.paired_device && st.paired_device !== "Connecting...")
          ? (st.paired_device + " (" + st.paired_ip + ")")
          : (st.paired_ip || "");
        document.getElementById("pairedIP").innerText = label;
        document.getElementById("nodeStatus").innerText = "PAIRED";
        document.getElementById("nodeStatus").style.background = "var(--primary)";
      } else {
        document.getElementById("pairedBox").style.display = "none";
        document.getElementById("unpairedBox").style.display = "block";
        document.getElementById("nodeStatus").innerText = "IDLE";
        document.getElementById("nodeStatus").style.background = "#334155";
      }

      // Update Web Share controls and visibility
      const isWebShareOn = !!st.web_share_enabled;
      const wsBadge = document.getElementById("webShareStatusBadge");
      const wsToggleBtn = document.getElementById("toggleWebShareBtn");
      const wsInactiveBox = document.getElementById("webShareInactiveBox");
      const wsActiveBox = document.getElementById("webShareActiveBox");
      const pinBadge = document.getElementById("webSharePINBadge");
      const btn6D = document.getElementById("webShare6DBtn");
      const btn4D = document.getElementById("webShare4DBtn");

      if (wsBadge) {
        wsBadge.innerText = isWebShareOn ? "ACTIVE" : "DISABLED";
        wsBadge.className = isWebShareOn ? "badge badge-online" : "badge badge-offline";
      }
      if (wsToggleBtn) {
        wsToggleBtn.innerText = isWebShareOn ? "⏹️ Turn OFF" : "▶️ Turn ON";
        wsToggleBtn.className = isWebShareOn ? "btn btn-danger" : "btn btn-success";
        wsToggleBtn.onclick = () => toggleWebShare(!isWebShareOn);
      }
      if (wsInactiveBox) wsInactiveBox.style.display = isWebShareOn ? "none" : "block";
      if (wsActiveBox) wsActiveBox.style.display = isWebShareOn ? "block" : "none";
      if (pinBadge) pinBadge.style.display = isWebShareOn ? "inline-block" : "none";
      if (btn6D) btn6D.style.display = isWebShareOn ? "inline-block" : "none";
      if (btn4D) btn4D.style.display = isWebShareOn ? "inline-block" : "none";

      if (isWebShareOn) {
        const host = window.location.host || ("localhost:" + (st.local_port || 19999));
        const fallbackUrl = window.location.protocol + "//" + host + "/share" + (st.web_share_pin ? ("?pin=" + st.web_share_pin) : "");
        const portalURL = st.portal_url || fallbackUrl;

        document.getElementById("localPortalURLText").innerText = portalURL;
        document.getElementById("localPortalLink").href = portalURL;
        if (st.portal_qr) {
          document.getElementById("localPortalQRImg").src = st.portal_qr;
        }
        if (st.web_share_pin && pinBadge) {
          pinBadge.innerText = "PIN: " + st.web_share_pin;
        }
        if (document.getElementById("webShareAutoAcceptCheck")) {
          document.getElementById("webShareAutoAcceptCheck").checked = !!st.web_share_auto_accept;
        }
      }
    }

    function renderPeers(peers) {
      const container = document.getElementById("peersList");
      container.innerHTML = "";
      if (!peers || peers.length === 0) {
        container.innerHTML = "<div style='color: var(--muted); font-size: 13px; text-align: center; padding: 20px;'>No other devices found on this network.</div>";
        return;
      }
      peers.forEach(p => {
        const div = document.createElement("div");
        div.className = "peer-item";
        div.innerHTML = ` + "`" + `
          <div>
            <div class="peer-info"><strong>${p.device_name || 'Device'}</strong> (${p.host_ip})</div>
            <div class="peer-sub">Role: ${p.role} | Port: ${p.port}</div>
          </div>
          <div class="peer-actions">
            <button class="btn btn-secondary" onclick="directPair('${p.host_ip}', this)">Pair</button>
            <button class="btn btn-success" onclick="directSend('${p.host_ip}')">Send</button>
          </div>
        ` + "`" + `;
        container.appendChild(div);
      });
    }

    function saveSettings() {
      const saveBtn = document.getElementById("saveSettingsBtn");
      const originalText = saveBtn.innerText;
      const newName = document.getElementById("deviceNameInput").value.trim();
      if (newName) {
        document.getElementById("headerDeviceName").innerText = "Device: " + newName;
      }
      const cfg = {
        device_name: newName,
        download_dir: document.getElementById("downloadDirInput").value,
        collision_policy: document.getElementById("collisionPolicySelect").value
      };
      sendCmd("set_config", cfg);
      saveBtn.innerText = "✓ Saved!";
      setTimeout(() => { saveBtn.innerText = originalText; }, 1200);
    }

    function scanNetwork() {
      sendCmd("scan");
    }

    let selectedNativePaths = [];
    let stagedBrowserFiles = [];
    let stagedServerPaths = null;
    let isStaging = false;

    function onDragOver(e) {
      e.preventDefault();
      e.stopPropagation();
      const zone = document.getElementById("dropZone");
      if (zone) zone.classList.add("dragover");
    }

    function onDragLeave(e) {
      e.preventDefault();
      e.stopPropagation();
      const zone = document.getElementById("dropZone");
      if (zone) zone.classList.remove("dragover");
    }

    function onDrop(e) {
      e.preventDefault();
      e.stopPropagation();
      const zone = document.getElementById("dropZone");
      if (zone) zone.classList.remove("dragover");
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
        onBrowserFilesSelected(e.dataTransfer.files);
      }
    }

    function triggerPickFiles() {
      const input = document.getElementById("browserFileInput");
      if (input) {
        input.value = "";
        input.click();
      }
    }

    function triggerPickFolder() {
      const input = document.getElementById("browserFolderInput");
      if (input) {
        input.value = "";
        input.click();
      }
    }

    function setNativeSelection(paths) {
      clearAllSelected(false);
      selectedNativePaths = paths;
      const box = document.getElementById("selectedFilesBox");
      const summary = document.getElementById("selectedFilesSummary");
      const list = document.getElementById("selectedFilesList");

      list.innerHTML = "";
      for (const p of paths) {
        const item = document.createElement("div");
        const cleanName = p.split('\\').pop().split('/').pop();
        item.innerText = "• " + cleanName + " (" + p + ")";
        list.appendChild(item);
      }

      summary.innerText = "✓ " + paths.length + (paths.length === 1 ? " item ready to send" : " items ready to send");
      box.style.display = "block";
      document.getElementById("sendPathInput").value = paths.join("\n");
    }

    function onBrowserFilesSelected(fileList) {
      if (!fileList || fileList.length === 0) return;
      const files = Array.from(fileList);
      if (files.length === 0) return;

      // Abort any existing background staging request
      if (currentStagingXHR) {
        currentStagingXHR.abort();
        currentStagingXHR = null;
      }
      isStaging = false;
      selectedNativePaths = [];
      stagedServerPaths = null;
      stagedBrowserFiles = files;

      const box = document.getElementById("selectedFilesBox");
      const summary = document.getElementById("selectedFilesSummary");
      const list = document.getElementById("selectedFilesList");

      let totalBytes = 0;
      list.innerHTML = "";
      for (const f of stagedBrowserFiles) {
        totalBytes += f.size;
        const item = document.createElement("div");
        const name = f.webkitRelativePath || f.name;
        item.innerText = "• " + name + " (" + formatMB(f.size) + ")";
        list.appendChild(item);
      }

      summary.innerText = "⏳ Staging " + stagedBrowserFiles.length + " file" + (stagedBrowserFiles.length > 1 ? "s" : "") + " (" + formatMB(totalBytes) + ")...";
      box.style.display = "block";
      document.getElementById("sendPathInput").value = "";

      // Background stage immediately so files are ready when user clicks Send
      startBackgroundStaging();
    }

    let currentStagingXHR = null;

    function startBackgroundStaging() {
      if (stagedBrowserFiles.length === 0) return;
      if (currentStagingXHR) {
        currentStagingXHR.abort();
        currentStagingXHR = null;
      }

      isStaging = true;
      const progressContainer = document.getElementById("stagingProgressBarContainer");
      const progressBar = document.getElementById("stagingProgressBar");
      if (progressContainer) progressContainer.style.display = "block";
      if (progressBar) progressBar.style.width = "0%";

      const formData = new FormData();
      let totalBytes = 0;
      for (const file of stagedBrowserFiles) {
        const relPath = file.webkitRelativePath || file.name;
        formData.append("files", file, relPath);
        totalBytes += file.size;
      }

      const summary = document.getElementById("selectedFilesSummary");
      if (summary) {
        summary.innerText = "⏳ Staging " + stagedBrowserFiles.length + " file(s) (0%)...";
      }

      const xhr = new XMLHttpRequest();
      currentStagingXHR = xhr;

      xhr.upload.onprogress = function(e) {
        if (e.lengthComputable) {
          const pct = Math.min(99, Math.round((e.loaded / e.total) * 100));
          if (progressBar) progressBar.style.width = pct + "%";
          if (summary) {
            summary.innerText = "⏳ Staging " + stagedBrowserFiles.length + " file(s) (" + pct + "% - " + formatMB(e.loaded) + " / " + formatMB(e.total) + ")";
          }
          const sendBtn = document.getElementById("sendTransferBtn");
          if (sendBtn && sendBtn.disabled) {
            sendBtn.innerText = "⏳ Staging (" + pct + "%)...";
          }
          const shareBtn = document.getElementById("shareWebBtn");
          if (shareBtn && shareBtn.disabled) {
            shareBtn.innerText = "⏳ Staging (" + pct + "%)...";
          }
        }
      };

      xhr.onload = function() {
        currentStagingXHR = null;
        isStaging = false;
        if (progressContainer) progressContainer.style.display = "none";
        if (xhr.status === 200) {
          try {
            const data = JSON.parse(xhr.responseText);
            stagedServerPaths = data.paths || [];
            if (summary) {
              summary.innerText = "✓ Ready to send (" + stagedBrowserFiles.length + " files - " + formatMB(totalBytes) + ")";
            }
          } catch (err) {
            if (summary) summary.innerText = "⚠️ Staging response parse error";
          }
        } else {
          if (summary) summary.innerText = "⚠️ Staging failed (HTTP " + xhr.status + ")";
        }
      };

      xhr.onerror = function() {
        currentStagingXHR = null;
        isStaging = false;
        if (progressContainer) progressContainer.style.display = "none";
        if (summary) summary.innerText = "⚠️ Staging network error";
      };

      xhr.onabort = function() {
        currentStagingXHR = null;
        isStaging = false;
        if (progressContainer) progressContainer.style.display = "none";
      };

      xhr.open("POST", "/api/upload");
      xhr.send(formData);
    }

    function clearAllSelected(clearInput = true) {
      if (currentStagingXHR) {
        currentStagingXHR.abort();
        currentStagingXHR = null;
      }
      isStaging = false;
      selectedNativePaths = [];
      stagedBrowserFiles = [];
      stagedServerPaths = null;

      // Re-enable send and share buttons if they were disabled waiting
      const sendBtn = document.getElementById("sendTransferBtn");
      if (sendBtn) {
        sendBtn.disabled = false;
        sendBtn.innerText = "🚀 Send to Paired Device";
      }
      const shareBtn = document.getElementById("shareWebBtn");
      if (shareBtn) {
        shareBtn.disabled = false;
        shareBtn.innerText = "🌐 Share to Web Portal";
      }

      const progressContainer = document.getElementById("stagingProgressBarContainer");
      if (progressContainer) progressContainer.style.display = "none";

      document.getElementById("browserFileInput").value = "";
      document.getElementById("browserFolderInput").value = "";
      document.getElementById("selectedFilesBox").style.display = "none";
      if (clearInput) {
        document.getElementById("sendPathInput").value = "";
      }
      sendCmd("clear_web_files");
    }

    // Backwards compatibility alias
    function clearBrowserFiles() {
      clearAllSelected(true);
    }

    function toggleManualPath() {
      const el = document.getElementById("manualPathGroup");
      const link = document.getElementById("togglePathLink");
      if (el.style.display === "none" || !el.style.display) {
        el.style.display = "block";
        link.innerText = "✕ Hide manual path input";
      } else {
        el.style.display = "none";
        link.innerText = "✏️ Advanced: Enter local path manually";
      }
    }

    function onManualPathInput() {
      selectedNativePaths = [];
      stagedBrowserFiles = [];
      stagedServerPaths = null;
    }

    function parsePaths() {
      const raw = document.getElementById("sendPathInput").value.trim();
      if (!raw) return [];
      const parts = raw.split(/\r?\n+/);
      const paths = [];
      for (const p of parts) {
        const clean = p.trim().replace(/^['"]|['"]$/g, '');
        if (clean) paths.push(clean);
      }
      return paths;
    }

    async function preparePathsForSend() {
      // 1. If native paths were selected via OS dialog
      if (selectedNativePaths && selectedNativePaths.length > 0) {
        return selectedNativePaths;
      }

      // 2. If browser files were selected
      if (stagedBrowserFiles.length > 0) {
        if (stagedServerPaths && stagedServerPaths.length > 0) {
          return stagedServerPaths;
        }

        const btn = document.getElementById("sendTransferBtn");
        const origText = btn ? btn.innerText : "Send";
        if (btn) {
          btn.innerText = "⏳ Staging files...";
          btn.disabled = true;
        }

        // Wait up to 60s for staging to finish (with abort check)
        const waitStart = Date.now();
        while (isStaging && (Date.now() - waitStart < 60000)) {
          await new Promise(r => setTimeout(r, 150));
        }

        if (stagedServerPaths && stagedServerPaths.length > 0) {
          if (btn) {
            btn.innerText = origText;
            btn.disabled = false;
          }
          return stagedServerPaths;
        }

        if (btn) {
          btn.innerText = origText;
          btn.disabled = false;
        }

        if (isStaging) {
          if (currentStagingXHR) currentStagingXHR.abort();
          alert("Staging timed out. Please try selecting the files again or use 'Choose Files' for instant local selection.");
        } else {
          alert("Failed to stage files for transfer. Please click '✕ Clear / Cancel' and select files again.");
        }
        return [];
      }

      // 3. Fall back to manual path input
      return parsePaths();
    }

    function pairNode() {
      const ip = document.getElementById("pairIPInput").value.trim();
      if (!ip) { alert("Please enter IP"); return; }
      const btn = document.getElementById("pairBtn");
      if (btn) {
        btn.innerText = "⏳ Pairing...";
        btn.disabled = true;
      }
      sendCmd("pair", { ip: ip });
    }

    function directPair(ip, btnEl) {
      if (btnEl) {
        btnEl.innerText = "⏳ Pairing...";
        btnEl.disabled = true;
      }
      sendCmd("pair", { ip: ip });
    }

    async function directSend(ip) {
      const paths = await preparePathsForSend();
      if (!paths || paths.length === 0) {
        alert("Please select files/folders or enter path(s) first!");
        return;
      }
      sendCmd("send", { paths: paths, target_ip: ip });
    }

    function disconnectNode() {
      sendCmd("disconnect");
    }

    let isSendingTransfer = false;
    async function sendTransfer() {
      if (isSendingTransfer) return;
      isSendingTransfer = true;
      const btn = document.getElementById("sendTransferBtn");
      const origText = btn ? btn.innerText : "";
      if (btn) btn.innerText = "⏳ Preparing...";
      try {
        const paths = await preparePathsForSend();
        if (!paths || paths.length === 0) {
          alert("Please select files/folders or enter path(s).");
          return;
        }
        const isPaired = document.getElementById("pairedBox").style.display === "block";
        const inputIP = document.getElementById("pairIPInput").value.trim();

        if (isPaired) {
          sendCmd("send", { paths: paths });
        } else if (inputIP) {
          sendCmd("send", { paths: paths, target_ip: inputIP });
        } else {
          alert("Please pair with a device first, or click 'Send' next to a device in the Nearby Devices list below.");
        }
      } finally {
        setTimeout(() => {
          isSendingTransfer = false;
          if (btn) btn.innerText = origText;
        }, 1000);
      }
    }

    function setPausedUI(isPaused) {
      isTransferPaused = isPaused;
      const pauseBtn = document.getElementById("pauseResumeBtn");
      if (!pauseBtn) return;
      if (isPaused) {
        pauseBtn.innerText = "▶️ Resume";
        pauseBtn.className = "btn btn-success";
        document.getElementById("nodeStatus").innerText = "PAUSED";
        document.getElementById("nodeStatus").style.background = "#f59e0b";
        document.getElementById("transferSpeed").innerText = "0.0 MB/s (Paused)";
        document.getElementById("transferETA").innerText = "Paused";
      } else {
        pauseBtn.innerText = "⏸️ Pause";
        pauseBtn.className = "btn btn-secondary";
        document.getElementById("nodeStatus").innerText = "TRANSFERRING";
        document.getElementById("nodeStatus").style.background = "var(--warning)";
      }
    }

    function togglePauseResume() {
      if (isTransferPaused) {
        sendCmd("resume");
      } else {
        sendCmd("pause");
      }
    }

    function cancelTransfer() {
      if (confirm("Are you sure you want to cancel the transfer?")) {
        sendCmd("cancel");
      }
    }

    let isWebSharePaused = false;
    function toggleWebSharePause() {
      if (isWebSharePaused) {
        isWebSharePaused = false;
        sendCmd("web_share_resume");
      } else {
        isWebSharePaused = true;
        sendCmd("web_share_pause");
      }
    }

    function cancelWebShareTransfer() {
      if (confirm("Stop and cancel the active Web Share transfer?")) {
        isWebSharePaused = false;
        sendCmd("web_share_cancel");
      }
    }

    function pauseCurrentFileAndNext() {
      if (activeFileIndex >= 0) {
        sendCmd("pause_file", { item_index: activeFileIndex });
      }
    }

    function pauseSpecificFile(idx) {
      sendCmd("pause_file", { item_index: idx });
    }

    function resumeSpecificFile(idx) {
      sendCmd("resume_file", { item_index: idx });
    }

    function skipCurrentFile() {
      if (confirm("Skip this file and move to the next?")) {
        sendCmd("skip_file", { item_index: activeFileIndex });
      }
    }

    function skipSpecificFile(idx) {
      sendCmd("skip_file", { item_index: idx });
    }

    function renderBatchQueue() {
      const container = document.getElementById("batchFilesQueueList");
      const box = document.getElementById("batchFilesQueueBox");
      if (!currentBatchFiles || currentBatchFiles.length <= 1) {
        box.style.display = "none";
        return;
      }
      box.style.display = "block";
      document.getElementById("batchQueueCountSpan").innerText = currentBatchFiles.length;
      container.innerHTML = "";

      currentBatchFiles.forEach(it => {
        const row = document.createElement("div");
        row.style = "display: flex; justify-content: space-between; align-items: center; padding: 5px 8px; border-bottom: 1px solid rgba(255,255,255,0.05);";

        let statusPill = '<span style="color:var(--muted); font-size:11px;">⏳ Pending</span>';
        let actionBtn = '<button class="btn btn-secondary" style="width:auto; padding:2px 6px; font-size:11px;" onclick="pauseSpecificFile(' + it.index + ')">⏸️ Pause</button>' +
                        '<button class="btn btn-secondary" style="width:auto; padding:2px 6px; font-size:11px;" onclick="skipSpecificFile(' + it.index + ')">✕ Skip</button>';

        if (it.status === "completed") {
          statusPill = '<span style="color:var(--success); font-size:11px;">✓ Done</span>';
          actionBtn = "";
        } else if (it.status === "transferring") {
          statusPill = '<span style="color:var(--warning); font-weight:600; font-size:11px;">⚡ Active</span>';
          actionBtn = '<button class="btn btn-secondary" style="width:auto; padding:2px 6px; font-size:11px; background:#d97706;" onclick="pauseSpecificFile(' + it.index + ')">⏸️ Pause</button>' +
                      '<button class="btn btn-secondary" style="width:auto; padding:2px 6px; font-size:11px; background:#e11d48;" onclick="skipSpecificFile(' + it.index + ')">✕ Skip</button>';
        } else if (it.status === "paused") {
          statusPill = '<span style="color:#f59e0b; font-weight:600; font-size:11px;">⏸️ Paused</span>';
          actionBtn = '<button class="btn btn-success" style="width:auto; padding:2px 6px; font-size:11px;" onclick="resumeSpecificFile(' + it.index + ')">▶️ Resume</button>' +
                      '<button class="btn btn-secondary" style="width:auto; padding:2px 6px; font-size:11px; background:#e11d48;" onclick="skipSpecificFile(' + it.index + ')">✕ Skip</button>';
        } else if (it.status === "skipped") {
          statusPill = '<span style="color:#94a3b8; text-decoration:line-through; font-size:11px;">⊘ Skipped</span>';
          actionBtn = "";
        }

        row.innerHTML =
          '<div style="overflow:hidden; text-overflow:ellipsis; white-space:nowrap; max-width:62%;">' +
            '<strong>#' + (it.index + 1) + '</strong> ' + it.rel_path + ' <small style="color:var(--muted)">(' + formatMB(it.size) + ')</small>' +
          '</div>' +
          '<div style="display:flex; gap:6px; align-items:center;">' +
            statusPill +
            actionBtn +
          '</div>';
        container.appendChild(row);
      });
    }

    let dirPickerTargetInput = null;
    let currentFSParent = "";

    function openDirPicker(targetInputId) {
      dirPickerTargetInput = targetInputId;
      const titleEl = document.getElementById("fsModalTitle");
      if (titleEl) {
        titleEl.innerText = (targetInputId === "sendPathInput") ? "📁 Select File or Folder to Send" : "📁 Select Destination Folder";
      }
      let initialDir = "";
      const curEl = document.getElementById(targetInputId);
      if (curEl && curEl.value) {
        const val = curEl.value.trim();
        if (val && !val.includes("\n")) {
          initialDir = val;
        }
      }
      document.getElementById("dirPickerModal").style.display = "flex";
      loadFSDir(initialDir);
    }

    function closeDirPicker() {
      document.getElementById("dirPickerModal").style.display = "none";
    }

    function selectPathForInput(selectedPath) {
      if (!selectedPath) return;
      if (dirPickerTargetInput) {
        clearBrowserFiles();
        document.getElementById(dirPickerTargetInput).value = selectedPath;
        if (dirPickerTargetInput === "downloadDirInput") {
          saveSettings();
        }
      }
      closeDirPicker();
    }

    function confirmSelectedDir() {
      const selectedPath = document.getElementById("fsCurrentPath").value.trim();
      selectPathForInput(selectedPath);
    }

    async function loadFSDir(dirPath) {
      const listEl = document.getElementById("fsDirsList");
      listEl.innerHTML = "<div style='color:var(--muted); padding:10px; font-size:13px;'>Loading...</div>";

      try {
        const url = "/api/fs/list" + (dirPath ? ("?dir=" + encodeURIComponent(dirPath)) : "");
        const res = await fetch(url);
        if (!res.ok) throw new Error("HTTP " + res.status);
        const data = await res.json();

        document.getElementById("fsCurrentPath").value = data.current_dir;
        currentFSParent = data.parent_dir;

        const upBtn = document.getElementById("fsUpBtn");
        upBtn.disabled = !data.parent_dir;
        upBtn.style.opacity = data.parent_dir ? "1" : "0.5";

        // Render Quick Jump roots (Android storage, Home, Downloads, Drives)
        const quickEl = document.getElementById("fsQuickDirs");
        quickEl.innerHTML = "";
        if (data.quick_dirs && data.quick_dirs.length > 0) {
          data.quick_dirs.forEach(qd => {
            const chip = document.createElement("button");
            chip.className = "quick-chip";
            chip.innerText = qd.name;
            chip.onclick = () => loadFSDir(qd.path);
            quickEl.appendChild(chip);
          });
        }

        // Render Directories and Files
        listEl.innerHTML = "";
        const isSendPicker = (dirPickerTargetInput === "sendPathInput");

        if (data.dirs && data.dirs.length > 0) {
          data.dirs.forEach(d => {
            const row = document.createElement("div");
            row.className = "dir-item";
            row.style.display = "flex";
            row.style.justifyContent = "space-between";
            row.style.alignItems = "center";
            const safePath = d.path.replace(/\\/g, '\\\\').replace(/'/g, "\\'");
            row.innerHTML = '<span style="cursor:pointer; flex:1;">📁 <span>' + d.name + '</span></span>' + 
              (isSendPicker ? '<button class="btn btn-secondary" style="width:auto; padding:2px 8px; font-size:11px;" onclick="event.stopPropagation(); selectPathForInput(\'' + safePath + '\')">Select Folder</button>' : '');
            row.onclick = () => loadFSDir(d.path);
            listEl.appendChild(row);
          });
        }

        if (isSendPicker && data.files && data.files.length > 0) {
          data.files.forEach(f => {
            const row = document.createElement("div");
            row.className = "dir-item";
            row.style.cursor = "pointer";
            row.style.display = "flex";
            row.style.justifyContent = "space-between";
            row.style.alignItems = "center";
            row.innerHTML = '<span style="overflow:hidden; text-overflow:ellipsis; white-space:nowrap; flex:1;">📄 ' + f.name + '</span> <span style="color:var(--muted); font-size:11px; margin-left:8px; white-space:nowrap;">' + formatMB(f.size) + '</span>';
            row.onclick = () => selectPathForInput(f.path);
            listEl.appendChild(row);
          });
        }

        if ((!data.dirs || data.dirs.length === 0) && (!data.files || data.files.length === 0)) {
          listEl.innerHTML = "<div style='color:var(--muted); padding:20px; text-align:center; font-size:13px;'>Empty folder. You can select this folder.</div>";
        }
      } catch (err) {
        listEl.innerHTML = "<div style='color:var(--danger); padding:10px; font-size:13px;'>Failed loading folder: " + err + "</div>";
      }
    }

    function navFSParent() {
      if (currentFSParent) {
        loadFSDir(currentFSParent);
      }
    }

    async function promptNewFolder() {
      const current = document.getElementById("fsCurrentPath").value.trim();
      if (!current) return;
      const name = prompt("Enter new folder name inside " + current + ":");
      if (!name || !name.trim()) return;

      try {
        const url = "/api/fs/mkdir?dir=" + encodeURIComponent(current) + "&name=" + encodeURIComponent(name.trim());
        const res = await fetch(url);
        const data = await res.json();
        if (data && data.path) {
          loadFSDir(data.path);
        }
      } catch (err) {
        alert("Failed creating folder: " + err);
      }
    }

    function showOfferModal(data) {
      const senderLabel = (data.device_name && data.device_name !== "Peer")
        ? (data.device_name + " (" + data.sender_ip + ")")
        : data.sender_ip;
      document.getElementById("modalTitle").innerText = data.is_batch ? "📁 Incoming Folder / Batch Transfer" : "📄 Incoming File Transfer";
      document.getElementById("modalDesc").innerText = senderLabel + " is offering: " + data.file_name + " (" + formatMB(data.file_size) + ")";
      document.getElementById("modalSaveDirInput").value = document.getElementById("downloadDirInput").value || ".";
      document.getElementById("offerModal").style.display = "flex";
    }

    function respondOffer(accept) {
      document.getElementById("offerModal").style.display = "none";
      const policy = document.getElementById("collisionPolicySelect").value;
      const dir = document.getElementById("modalSaveDirInput").value || document.getElementById("downloadDirInput").value || ".";
      sendCmd("respond_offer", { accept: accept, collision_policy: policy, save_dir: dir });
    }

    // Initialize WebSocket on page load
    window.addEventListener("DOMContentLoaded", connectWS);
  </script>
</body>
</html>
`
