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
    input, select { width: 100%; padding: 10px 12px; background: #0b1120; border: 1px solid var(--border); border-radius: 8px; color: var(--text); font-size: 14px; outline: none; }
    input:focus, select:focus { border-color: var(--primary); }
    .btn { display: inline-flex; align-items: center; justify-content: center; padding: 10px 16px; font-size: 14px; font-weight: 600; border-radius: 8px; border: none; cursor: pointer; transition: 0.15s; background: var(--primary); color: white; width: 100%; }
    .btn:hover { background: var(--primary-hover); }
    .btn-danger { background: var(--danger); }
    .btn-danger:hover { background: #dc2626; }
    .btn-success { background: var(--success); }
    .btn-success:hover { background: #059669; }
    .btn-secondary { background: #334155; }
    .btn-secondary:hover { background: #475569; }
    .peer-item { display: flex; justify-content: space-between; align-items: center; padding: 10px 12px; background: #0b1120; border: 1px solid var(--border); border-radius: 8px; margin-bottom: 8px; }
    .peer-info { font-size: 14px; }
    .peer-sub { font-size: 12px; color: var(--muted); }
    .peer-actions { display: flex; gap: 8px; }
    .peer-actions .btn { width: auto; padding: 6px 12px; font-size: 12px; }
    .progress-box { margin-top: 10px; }
    .progress-bar-container { width: 100%; height: 12px; background: #0b1120; border-radius: 9999px; overflow: hidden; margin: 10px 0; border: 1px solid var(--border); }
    .progress-bar { height: 100%; width: 0%; background: linear-gradient(90deg, #3b82f6, #60a5fa); transition: width 0.1s ease; }
    .stats-row { display: flex; justify-content: space-between; font-size: 13px; color: var(--muted); }
    .log-box { background: #050811; border: 1px solid var(--border); border-radius: 8px; height: 180px; overflow-y: auto; padding: 12px; font-family: monospace; font-size: 12px; color: #cbd5e1; }
    .log-entry { margin-bottom: 4px; }
    .log-time { color: var(--muted); }
    .modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.7); display: none; align-items: center; justify-content: center; z-index: 100; backdrop-filter: blur(4px); }
    .modal { background: var(--card); border: 1px solid var(--border); border-radius: 16px; max-width: 440px; width: 90%; padding: 24px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.5); text-align: center; }
    .modal h3 { font-size: 20px; margin-bottom: 8px; }
    .modal p { color: var(--muted); font-size: 14px; margin-bottom: 20px; }
    .modal-btns { display: flex; gap: 12px; }
  </style>
</head>
<body>
  <div class="container">
    <header>
      <div class="logo">⚡ med<span>Xfer</span> <small style="font-size: 13px; color: var(--muted); font-weight: 400; margin-left: 8px;">Headless API Dashboard</small></div>
      <div id="connectionStatus" class="badge badge-offline">Connecting...</div>
    </header>

    <div class="grid">
      <!-- Device & Settings Card -->
      <div class="card">
        <h2>Settings & Status <span id="nodeStatus" class="badge" style="background:#334155;">IDLE</span></h2>
        <div class="form-group">
          <label>Device Name</label>
          <input type="text" id="deviceNameInput">
        </div>
        <div class="form-group">
          <label>Download Directory</label>
          <input type="text" id="downloadDirInput">
        </div>
        <div class="form-group">
          <label>Collision Policy</label>
          <select id="collisionPolicySelect">
            <option value="auto_rename">Auto-Rename (file (1).ext)</option>
            <option value="overwrite">Overwrite existing</option>
            <option value="skip">Smart Skip existing</option>
          </select>
        </div>
        <button class="btn btn-secondary" onclick="saveSettings()">Save Settings</button>

        <div style="margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--border);">
          <div id="pairedBox" style="display: none;">
            <div style="font-size: 14px; margin-bottom: 10px;">🟢 Paired with: <strong id="pairedIP"></strong></div>
            <button class="btn btn-danger" onclick="disconnectNode()">Disconnect</button>
          </div>
          <div id="unpairedBox">
            <label>Direct Connect / Pair IP</label>
            <div style="display: flex; gap: 8px;">
              <input type="text" id="pairIPInput" placeholder="192.168.1.50">
              <button class="btn" style="width: auto;" onclick="pairNode()">Pair</button>
            </div>
          </div>
        </div>
      </div>

      <!-- Network Discovery & Transfer Card -->
      <div class="card">
        <h2>Nearby Devices <button class="btn btn-secondary" style="width: auto; padding: 4px 10px; font-size: 12px;" onclick="scanNetwork()">🔄 Scan</button></h2>
        <div id="peersList" style="min-height: 110px; margin-bottom: 16px;">
          <div style="color: var(--muted); font-size: 13px; text-align: center; padding: 20px;">Click "Scan" to discover devices on your Wi-Fi network</div>
        </div>

        <h2 style="margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--border);">Send File or Folder</h2>
        <div class="form-group">
          <label>Local File/Folder Path</label>
          <input type="text" id="sendPathInput" placeholder="e.g. C:\Downloads\video.mp4 or ./my_folder">
        </div>
        <button class="btn btn-success" onclick="sendTransfer()">🚀 Send Transfer</button>
      </div>
    </div>

    <!-- Active Transfer Telemetry Card -->
    <div class="card" id="transferCard" style="display: none; margin-bottom: 20px;">
      <h2>Live Transfer Telemetry <button class="btn btn-danger" style="width: auto; padding: 4px 10px; font-size: 12px;" onclick="cancelTransfer()">Cancel</button></h2>
      <div style="font-size: 15px; font-weight: 600;" id="transferFileName">File: test.mp4</div>
      <div class="progress-bar-container">
        <div class="progress-bar" id="progressBar"></div>
      </div>
      <div class="stats-row">
        <div>Progress: <span id="progressPercent">0%</span> (<span id="fileIndexSpan">1/1</span>)</div>
        <div>Speed: <span id="transferSpeed">0 MB/s</span></div>
        <div>ETA: <span id="transferETA">0s</span></div>
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
      <div class="modal-btns">
        <button class="btn btn-secondary" onclick="respondOffer(false)">Reject</button>
        <button class="btn btn-success" onclick="respondOffer(true)">Accept & Download</button>
      </div>
    </div>
  </div>

  <script>
    let ws = null;
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
          document.getElementById("pairedBox").style.display = "block";
          document.getElementById("unpairedBox").style.display = "none";
          document.getElementById("pairedIP").innerText = msg.data.ip;
          document.getElementById("nodeStatus").innerText = "PAIRED";
          document.getElementById("nodeStatus").style.background = "var(--primary)";
          break;
        case "disconnected":
          document.getElementById("pairedBox").style.display = "none";
          document.getElementById("unpairedBox").style.display = "block";
          document.getElementById("nodeStatus").innerText = "IDLE";
          document.getElementById("nodeStatus").style.background = "#334155";
          break;
        case "incoming_offer":
          showOfferModal(msg.data);
          break;
        case "transfer_start":
          document.getElementById("transferCard").style.display = "block";
          document.getElementById("transferFileName").innerText = "File: " + msg.data.current_file;
          document.getElementById("fileIndexSpan").innerText = msg.data.file_index + "/" + msg.data.total_files;
          document.getElementById("nodeStatus").innerText = "TRANSFERRING";
          document.getElementById("nodeStatus").style.background = "var(--warning)";
          break;
        case "transfer_progress":
          document.getElementById("transferCard").style.display = "block";
          document.getElementById("progressBar").style.width = msg.data.file_percent.toFixed(1) + "%";
          document.getElementById("progressPercent").innerText = msg.data.file_percent.toFixed(1) + "%";
          document.getElementById("transferSpeed").innerText = msg.data.speed_mbps.toFixed(1) + " MB/s";
          document.getElementById("transferETA").innerText = msg.data.eta_seconds + "s";
          break;
        case "transfer_complete":
        case "transfer_canceled":
        case "transfer_rejected":
          document.getElementById("transferCard").style.display = "none";
          document.getElementById("nodeStatus").innerText = document.getElementById("pairedBox").style.display === "block" ? "PAIRED" : "IDLE";
          document.getElementById("nodeStatus").style.background = document.getElementById("pairedBox").style.display === "block" ? "var(--primary)" : "#334155";
          break;
      }
    }

    function updateStatusUI(st) {
      if (!st) return;
      document.getElementById("deviceNameInput").value = st.device_name || "";
      document.getElementById("downloadDirInput").value = st.download_dir || "";
      document.getElementById("collisionPolicySelect").value = st.collision_policy || "auto_rename";

      if (st.paired) {
        document.getElementById("pairedBox").style.display = "block";
        document.getElementById("unpairedBox").style.display = "none";
        document.getElementById("pairedIP").innerText = st.paired_ip || "";
        document.getElementById("nodeStatus").innerText = "PAIRED";
        document.getElementById("nodeStatus").style.background = "var(--primary)";
      } else {
        document.getElementById("pairedBox").style.display = "none";
        document.getElementById("unpairedBox").style.display = "block";
        document.getElementById("nodeStatus").innerText = "IDLE";
        document.getElementById("nodeStatus").style.background = "#334155";
      }
    }

    function renderPeers(peers) {
      const container = document.getElementById("peersList");
      container.innerHTML = "";
      if (!peers || peers.length === 0) {
        container.innerHTML = "<div style='color: var(--muted); font-size: 13px; text-align: center; padding: 20px;'>No devices found.</div>";
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
            <button class="btn btn-secondary" onclick="directPair('${p.host_ip}')">Pair</button>
            <button class="btn btn-success" onclick="directSend('${p.host_ip}')">Send</button>
          </div>
        ` + "`" + `;
        container.appendChild(div);
      });
    }

    function saveSettings() {
      const cfg = {
        device_name: document.getElementById("deviceNameInput").value,
        download_dir: document.getElementById("downloadDirInput").value,
        collision_policy: document.getElementById("collisionPolicySelect").value
      };
      sendCmd("set_config", cfg);
    }

    function scanNetwork() {
      sendCmd("scan");
    }

    function pairNode() {
      const ip = document.getElementById("pairIPInput").value.trim();
      if (!ip) { alert("Please enter IP"); return; }
      sendCmd("pair", { ip: ip });
    }

    function directPair(ip) {
      sendCmd("pair", { ip: ip });
    }

    function directSend(ip) {
      const path = document.getElementById("sendPathInput").value.trim();
      if (!path) { alert("Please enter a file or folder path in the input box first!"); return; }
      sendCmd("send", { paths: [path], target_ip: ip });
    }

    function disconnectNode() {
      sendCmd("disconnect");
    }

    function sendTransfer() {
      const path = document.getElementById("sendPathInput").value.trim();
      if (!path) { alert("Please specify a file or folder path."); return; }
      sendCmd("send", { paths: [path] });
    }

    function cancelTransfer() {
      sendCmd("cancel");
    }

    function showOfferModal(data) {
      document.getElementById("modalTitle").innerText = data.is_batch ? "📁 Incoming Folder Transfer" : "📄 Incoming File Transfer";
      document.getElementById("modalDesc").innerText = (data.device_name || data.sender_ip) + " is offering: " + data.file_name + " (" + (data.file_size / (1024*1024)).toFixed(2) + " MB)";
      document.getElementById("offerModal").style.display = "flex";
    }

    function respondOffer(accept) {
      document.getElementById("offerModal").style.display = "none";
      sendCmd("respond_offer", { accept: accept });
    }

    // Initialize WebSocket on page load
    window.addEventListener("DOMContentLoaded", connectWS);
  </script>
</body>
</html>
`
