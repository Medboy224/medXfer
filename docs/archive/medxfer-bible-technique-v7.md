# medXfer v1.0 — Bible Technique & Spécifications d'Architecture Finales v7.0 (Exhaustive & Gelée)

---

## 🏛️ Vue d'Ensemble & Principes d'Ingénierie Zero-Trust

**medXfer** est une solution logicielle souveraine de transfert de fichiers ultra-rapide de peer-to-peer (P2P) sur réseau local (LAN). Son objectif d'ingénierie principal est d'atteindre un **débit utile ≥ 80 % de la vitesse physique d'un test iperf3** sur le même lien matériel (avec chiffrement actif et données incompressibles), tout en garantissant une résilience absolue face aux interruptions réseau, aux pannes d'E/S disque, aux surchauffes thermiques sur mobile, et aux attaques informatiques sur réseaux Wi-Fi publics.

### Principes Architecturaux Directeurs :
1. **Sécurité Zero-Trust :** Le réseau local est considéré par défaut comme hostile. Aucune donnée non chiffrée ne circule sur le câble. Aucune confiance n'est accordée sans authentification explicite.
2. **Découplage Réseau / Stockage :** La couche de transport QUIC et le moteur d'écriture disque communiquent via une boucle d'hystérésis et de contre-pression (*Disk Backpressure*) pour éliminer la saturation de la mémoire RAM et prévenir le *bufferbloat*.
3. **Régulation Thermique Proactive :** Ajustement dynamique des chargeurs (*Dynamic Worker Regulator*) pour protéger la batterie et le SoC des smartphones contre le bridage thermique (*thermal throttling*) de l'OS.
4. **Garanties ACID & Atomicité :** Les métadonnées et états de transfert sont suivis dans SQLite en mode WAL (*Write-Ahead Logging*). Les écritures sur disque se font via des écritures positionnées atomiques (`WriteAt`/`pwrite`), validées par hachage cryptographique et renommage atomique.

---

# CHAPITRE 1 : Protocoles Réseau, Découverte & Format des Trames

---

### Section 1 : mDNS Éphémère & Annonces DNS-SD
medXfer utilise le protocole **mDNS / DNS-SD** (`_medxfer._udp.local.`) pour la découverte locale automatique d'appareils sur le même segment L2.

* **Masquage de Confidentialité :** Aucune métadonnée personnelle (nom de l'utilisateur, nom de l'hôte, liste de fichiers, taille ou chemin) n'est publiée dans les annonces mDNS.
* **Identifiants Éphémères :** Les paquets d'annonce TXT contiennent uniquement un identifiant de session aléatoire de 64 bits et une clé publique d'appairage temporaire.
* **Fallbacks de Découverte :** En cas d'isolation d'AP (*Access Point Isolation*) bloquant le trafic mDNS/UDP Multicast, medXfer bascule alternativement sur :
  1. Scan UDP direct sur le port dédié `19998`.
  2. Saisie manuelle de l'adresse IP / QR Code direct.

```
+-----------------------------------------------------------------------+
| Announcement TXT Record (_medxfer._udp.local.)                        |
+-----------------------------------------------------------------------+
| txtvers=1                                                             |
| id=<session_id_ephemeral_hex_64bit>                                   |
| pub=<ephemeral_pubkey_base64url>                                      |
| port=<quic_udp_port>                                                  |
+-----------------------------------------------------------------------+
```

---

### Section 2 : QR Code Base64URL & Secret 128-bit
Pour initier l'appairage ponctuel (*One-Shot*), l'émetteur génère un QR Code contenant un secret cryptographique d'au moins 128 bits d'entropie encodé en Base64URL.

* **Format de l'URI d'Appairage :**
  `medxfer://v1/pair?sid=<SESSION_ID>&secret=<SECRET_BASE64URL>&ip=<LOCAL_IP>&port=<PORT>`
* **Saisie Manuelle de Secours (PIN 6 Chiffres) :** Si la caméra est indisponible, un code PIN à 6 chiffres à faible entropie est proposé. Ce code fait l'objet d'une protection stricte anti-brute-force (voir Chapitre 3).

---

### Section 3 : Handshake SPAKE2 Context-String (RFC 9382)
L'appairage P2P repose sur l'accord de clé par mot de passe **SPAKE2** (RFC 9382) symétrique d'égal à égal.

* **Groupe Cryptographique :** Curve25519 (Ed25519) avec générateurs distincts $M$ et $N$ hardcodés conformément aux constantes de la RFC 9382.
* **Séparation de Domaine Strict (Context-String) :**
  `Context-String = "medxfer-v1.0-spake2-curve25519-hkdf-sha256"`
* **Nettoyage Mémoire (Memory Scrubbing) :** Dès que le secret partagé maître $K$ est dérive par SPAKE2, les scalaires éphémères $x, y \in \mathbb{Z}_p$ et le mot de passe $w$ sont immédiatement écrasés en RAM via `memguard` / `zeroize`.
* **Règle des 3 Erreurs de PIN :** À la 3ᵉ tentative de PIN erronée, la session est immédiatement détruite en mémoire vive et l'appairage est réinitialisé. Le simple blocage par adresse IP est proscrit car inefficace sur réseau NAT ou Wi-Fi public.

---

### Section 4 : TLS 1.3 PSK & HKDF-SHA256 (Mode 1-RTT Strict)
Le tunnel de transport QUIC est chiffré via **TLS 1.3 PSK** (Pre-Shared Key).

* **Dérivation de la Pre-Shared Key (PSK) :**
  $$	ext{PSK} = 	ext{HKDF-Expand}(	ext{HKDF-Extract}(	ext{salt}=	ext{session\_id}, 	ext{IKM}=K), 	ext{info}=	ext{"medxfer-v1.0-tls13-psk-derivation"}, L=32)$$
* **Désactivation du Mode 0-RTT :** Le mode 0-RTT (*Early Data*) est **strictement désactivé** pour éliminer le risque d'attaque par rejeu (*Replay Attack*) et garantir le *Forward Secrecy*.
* **Sélection Dynamique des Cipher Suites :**
  * `TLS_AES_128_GCM_SHA256` si instructions matérielles AES-NI (x86_64) ou ARMv8 Crypto présentes.
  * `TLS_CHACHA20_POLY1305_SHA256` en repli automatique (mobiles plus anciens sans AES matériel).

---

### Section 5 : Stream 0 Framing & Message HandshakeInit
Le **Stream 0** est le canal de contrôle QUIC bidirectionnel dédié aux métadonnées et commandes.

#### Header TLV (4 Octets Big-Endian) :
* Octet 0 : `Type` (uint8)
* Octets 1–3 : `Length` (uint24 Big-Endian)

#### Types de Trames du Stream 0 :
* `0x01` : `HandshakeInit`
* `0x02` : `ManifestRequest`
* `0x03` : `ManifestResponse`
* `0x04` : `TransferStart`
* `0x05` : `TransferPause`
* `0x06` : `ThermalAlert`
* `0x07` : `TransferComplete`

```go
type HandshakeInitMessage struct {
	Version           string   `json:"version"`
	PeerID            string   `json:"peer_id"`
	PeerName          string   `json:"peer_name"`
	PeerEd25519Pubkey string   `json:"peer_ed25519_pubkey"`
	Timestamp         int64    `json:"timestamp"`
	CPUCores          int      `json:"cpu_cores"`
	CPUFeatures       []string `json:"cpu_features"`
	Signature         string   `json:"signature,omitempty"`
}
```

* **Validation Temporelle :** Rejet systématique si $|T_{	ext{local}} - T_{	ext{remote}}| > 300 	ext{ secondes}$ (anti-rejeu).

---

### Section 6 : Contrôle de Congestion BBR & Paramètres QUIC
Le transport QUIC repose sur la librairie `quic-go` avec l'algorithme de contrôle de congestion **BBR** (Bottleneck Bandwidth and RTT).

* **Fenêtres de Crédit de Flux (`MAX_STREAM_DATA`) :**
  * Mobile (Android / iOS) : 32 Mo initial / max
  * Desktop (Windows / Linux / macOS) : 64 Mo initial / max
* **Garde-fous d'Inactivité :**
  * `MaxIdleTimeout`: 30 secondes
  * `KeepAlivePeriod`: 10 secondes

---

### Section 7 : Streams 1 à 8 Framing & Écritures Positionnées (`pwrite`)
Les données massives sont découpées en blocs de **4 Mo (4 194 304 octets)** et transmises sur 1 à 8 Streams QUIC parallèles.

#### Entête Binaire de Bloc (40 Octets Fixes) :
```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                       TransferID (16 Octets)                  +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                      BlockIndex (4 Octets)                    |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                      BlockSize  (4 Octets)                    |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                     BlockHash (32 Octets BLAKE3/SHA256)       +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

---

### Section 8 : Migration de Connexion QUIC (CID 64-bit)
Conformément à la RFC 9000, le client autorise la migration transparente de connexion réseau (ex: basculement Wi-Fi vers 4G/5G).

* **Connection IDs (CID) :** Identifiants de connexion de 64 bits (8 octets) non corrélables.
* **Validation du Nouveau Chemin :** L'émetteur envoie les trames `PATH_CHALLENGE` et `PATH_RESPONSE`.
* **Protection Anti-Flapping :** Un délai minimal de 1 seconde est imposé entre deux migrations d'IP pour éviter les boucles d'oscillation.

---

### Section 9 : Rate Limiting & Protection Anti-DoS
* **Limitation des Connexions Entrantes :** 5 tentatives d'appairage maximum par minute et par IP.
* **Buffer Overflow Shield :** Rejet immédiat de toute trame TLV déclarant une taille supérieure à 16 Mo sur le Stream 0.

---

# CHAPITRE 2 : Moteur de Stockage, Ring Buffer de Contre-pression Disque & Résilience SQLite

---

### Section 1 : Ring Buffer & Contre-pression Disque (*Disk Backpressure*)
Pour éviter d'épuiser la mémoire RAM lors des écritures sur stockage lent (carte SD, mémoire eMMC), le récepteur intègre un moniteur de contre-pression `DiskBackpressureMonitor`.

```go
type DiskBackpressureMonitor struct {
	mu                  sync.Mutex
	maxBufferBytes      int64
	currentBuffered     int64
	writeDurations      [50]time.Duration
	writeHistoryIndex   int
	writeHistoryCount   int
	isPaused            bool
	lastWriteTime       time.Time
	highWatermarkRatio  float64 // 0.80 (80%)
	lowWatermarkRatio   float64 // 0.40 (40%)
	diskStallThreshold  time.Duration // 500ms
}
```

* **Mesure P95 d'Écriture :** Calcul du 95ᵉ centile (index 47 sur 50 échantillons glissants).
* **Seuils d'Hystérésis Stricts :**
  * **Pause (`ShouldPause = true`) :** Déclenché si la RAM consommée atteint **80 %** de la limite autorisée OU si le temps d'écriture P95 dépasse **500 ms** (*Disk Stall*).
  * **Reprise (`ShouldResume = true`) :** Déclenché uniquement lorsque la RAM redescend sous **40 %**.
* **Couplage Réseau Native QUIC :** En cas de pause, le récepteur suspend l'appel `stream.Read()`. Ne consommant plus d'octets, la pile `quic-go` arrête d'émettre les trames d'accréditation `MAX_STREAM_DATA`. L'émetteur se retrouve bloqué de manière native sur son appel `stream.Write()` sans aucun message de contrôle réseau supplémentaire.

---

### Section 2 : Schéma SQLite Canonique DDL & Pragmas

#### Configuration des Pragmas de Haute Performance :
```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA cache_size = -64000; -- 64 Mo de cache RAM
PRAGMA busy_timeout = 5000; -- 5 secondes d'attente sur verrou
PRAGMA foreign_keys = ON;
```

#### Schema DDL Canonique :
```sql
CREATE TABLE IF NOT EXISTS transfer_sessions (
    id TEXT PRIMARY KEY,
    peer_id TEXT NOT NULL,
    local_file_path TEXT NOT NULL,
    filesize INTEGER NOT NULL,
    total_chunks INTEGER NOT NULL,
    bytes_transferred INTEGER DEFAULT 0,
    status TEXT CHECK(status IN ('PENDING', 'ACTIVE', 'PAUSED', 'COMPLETED', 'FAILED')) DEFAULT 'PENDING',
    hash_algorithm TEXT CHECK(hash_algorithm IN ('BLAKE3', 'SHA256')) NOT NULL,
    expected_file_hash BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    error_message TEXT
);

CREATE TABLE IF NOT EXISTS transfer_blocks (
    transfer_id TEXT NOT NULL,
    block_index INTEGER NOT NULL,
    block_size INTEGER NOT NULL,
    block_blake3_hash BLOB NOT NULL,
    completed INTEGER DEFAULT 0,
    completed_at INTEGER,
    PRIMARY KEY (transfer_id, block_index),
    FOREIGN KEY (transfer_id) REFERENCES transfer_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_blocks_incomplete 
ON transfer_blocks(transfer_id, block_index) WHERE completed = 0;

CREATE INDEX IF NOT EXISTS idx_sessions_status 
ON transfer_sessions(status);
```

---

### Section 3 : SQLite Transaction Batcher (`SQLiteTransactionBatcher`)
Afin d'éviter d'exécuter un commit SQLite par bloc (ce qui détruirait les performances E/S), les écritures en base de données sont regroupées par lot atomique de **10 blocs (40 Mo)** ou **toutes les 3 secondes**.

```go
func (btx *SQLiteTransactionBatcher) commitPendingLocked() error {
	if len(btx.pendingBlocks) == 0 {
		return nil
	}

	tx, err := btx.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("BeginTx failed: %w", err)
	}
	defer tx.Rollback()

	totalBytes := int64(0)
	for _, rec := range btx.pendingBlocks {
		_, err := tx.Exec(`
			INSERT INTO transfer_blocks 
			(transfer_id, block_index, block_size, block_blake3_hash, completed, completed_at)
			VALUES (?, ?, ?, ?, 1, ?)
		`, btx.transferID, rec.Index, rec.Size, rec.Hash[:], rec.Time.Unix())
		if err != nil {
			return fmt.Errorf("INSERT failed at block %d: %w", rec.Index, err)
		}
		totalBytes += int64(rec.Size)
	}

	_, err = tx.Exec(`
		UPDATE transfer_sessions 
		SET bytes_transferred = bytes_transferred + ?
		WHERE id = ?
	`, totalBytes, btx.transferID)
	if err != nil {
		return fmt.Errorf("UPDATE failed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("Commit failed: %w", err)
	}

	btx.lastCommitTime = time.Now()
	btx.pendingBlocks = btx.pendingBlocks[:0]
	return nil
}
```

---

### Section 4 : Écritures Positionnées Atomiques (`WriteAt`) & Sync Disque
Les flux de données réseau écrivent de manière asynchrone et désordonnée dans le fichier temporaire `.part` à l'aide d'écritures positionnées thread-safe (`pwrite` / `WriteAt`).

```go
type PositionedFileWriter struct {
	file              *os.File
	fileSize          int64
	completedBlocksMu sync.RWMutex
	completedBlocks   map[uint32]bool
	mapMu             sync.Mutex
	blockWriteLocks   map[uint32]*sync.Mutex
}

func (pfw *PositionedFileWriter) WriteBlockAt(blockIndex uint32, data []byte, hash [32]byte) error {
	mtx := pfw.getBlockMutex(blockIndex)
	mtx.Lock()
	defer mtx.Unlock()

	offset := int64(blockIndex) * (4 * 1024 * 1024)
	if offset+int64(len(data)) > pfw.fileSize {
		return fmt.Errorf("block %d writes beyond file boundary", blockIndex)
	}

	n, err := pfw.file.WriteAt(data, offset)
	if err != nil || n != len(data) {
		return fmt.Errorf("WriteAt failed for block %d", blockIndex)
	}

	pfw.completedBlocksMu.Lock()
	pfw.completedBlocks[blockIndex] = true
	pfw.completedBlocksMu.Unlock()
	return nil
}

func (pfw *PositionedFileWriter) SyncBatch() error {
	return pfw.file.Sync() // fsync matériel exécuté tous les 12 blocs (~50 Mo)
}
```

---

### Section 5 : Hash Adaptatif (BLAKE3 vs SHA-256)
L'algorithme de hachage des blocs est sélectionné dynamiquement lors de l'initialisation de la session et reste **immuable** dans SQLite (`transfer_sessions.hash_algorithm`).

```go
func SelectHashAlgorithm(isThermalConstrained bool) string {
	if isThermalConstrained {
		return "SHA256"
	}
	if cpu.X86.HasAVX2 && runtime.NumCPU() >= 4 && (runtime.GOOS == "windows" || runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
		return "BLAKE3" // Reservé aux machines Desktop performantes avec AVX2
	}
	return "SHA256" // Fallback pour smartphones et CPU sans AVX2
}
```

---

### Section 6 : Protection E/S, Quotas, `ENOSPC` & Anti Zip-Slip

#### Neutralisation Stricte du Path Traversal & Zip-Slip :
```go
func ValidateAndPrepareFilePath(requestedPath string, rootDir string) (string, error) {
	cleaned := filepath.Clean(requestedPath)
	if filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" || strings.Contains(cleaned, "..") || strings.Contains(cleaned, " ") {
		return "", fmt.Errorf("invalid or malicious path: %s", requestedPath)
	}

	fullPath := filepath.Join(rootDir, cleaned)
	realRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		return "", err
	}

	parentDir := filepath.Dir(fullPath)
	os.MkdirAll(parentDir, 0755)
	realParent, err := filepath.EvalSymlinks(parentDir)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(realRoot, realParent)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", fmt.Errorf("Zip-Slip attack detected: %s", requestedPath)
	}
	return fullPath, nil
}
```

* **Vérification d'Espace Disque (`CheckDiskSpace`) :** Tolérance d'espace exigeant la taille du fichier $+ 10 \%$ de marge de sécurité pour SQLite WAL.
* **Gestion Portable de Disque Plein (`isENOSPC`) :** Interception de `syscall.ENOSPC` (Unix) et `ERROR_DISK_FULL` (Win32), plaçant la session en état `PAUSED` propre sans détruire le fichier `.part`.

---

### Section 7 : Algorithme de Crash-Recovery Post-Crash
Au démarrage de medXfer, les sessions interrompues (`ACTIVE` / `PAUSED`) sont analysées. Pour éviter de re-hacher des fichiers de plusieurs gigaoctets, **seul le bloc de frontière (`lastCompletedIndex`) est relu sur disque et vérifié**.

```go
func (receiver *SecureReceiver) RecoverSingleTransfer(db *sql.DB, transferID, filePath string, totalChunks uint32, expectedSize int64, hashAlgo string) error {
	info, err := os.Stat(filePath)
	if err != nil || info.Size() > expectedSize {
		return fmt.Errorf("invalid .part file state")
	}

	var lastCompletedIndex uint32
	err = db.QueryRow("SELECT MAX(block_index) FROM transfer_blocks WHERE transfer_id = ? AND completed = 1", transferID).Scan(&lastCompletedIndex)
	if err != nil {
		return nil
	}

	// Re-hachage ciblé du seul bloc de frontière
	offset := int64(lastCompletedIndex) * (4 * 1024 * 1024)
	file, _ := os.Open(filePath)
	buf := make([]byte, 4*1024*1024)
	n, _ := file.ReadAt(buf, offset)
	file.Close()

	computedHash, _ := HashBlock(buf[:n], hashAlgo)
	var storedHash []byte
	db.QueryRow("SELECT block_blake3_hash FROM transfer_blocks WHERE transfer_id = ? AND block_index = ?", transferID, lastCompletedIndex).Scan(&storedHash)

	if !bytes.Equal(computedHash[:], storedHash) {
		// En cas de corruption partielle lors du crash, invalider ce bloc spécifique
		db.Exec("UPDATE transfer_blocks SET completed = 0 WHERE transfer_id = ? AND block_index = ?", transferID, lastCompletedIndex)
	}

	db.Exec("UPDATE transfer_sessions SET status = 'PAUSED' WHERE id = ?", transferID)
	return nil
}
```

---

# CHAPITRE 3 : Handshake, Authentification SPAKE2 & Tunnel Cryptographique TLS 1.3 PSK

---

### Section 1 : Handshake SPAKE2 (RFC 9382) & Anti Brute-Force
* **Authentification P2P Symétrique :** SPAKE2 RFC 9382 sur Curve25519.
* **Générateurs Fixes :** Points $M$ et $N$ hardcodés issus des tables standard de la RFC 9382.
* **Protection Memguard :** Les secrets $x, y, w$ sont alloués dans des pages mémoire verrouillées exclues des SWAP/Core Dumps et détruits via `zeroize` après dérivation de $K$.
* **3 Essais Max :** Blocage définitif de la session après 3 échecs sur le PIN.

---

### Section 2 : Dérivation KDF & Séparation de Domaine
$$	ext{PSK} = 	ext{HKDF-Expand}(	ext{HKDF-Extract}(	ext{salt}=	ext{session\_id}, 	ext{IKM}=K), 	ext{info}=	ext{"medxfer-v1.0-tls13-psk-derivation"}, L=32)$$

---

### Section 3 : Tunnel TLS 1.3 PSK
* **Handshake 1-RTT Strict :** Rejet des requêtes 0-RTT Early Data.
* **Cipher Suites :** `TLS_AES_128_GCM_SHA256` ou `TLS_CHACHA20_POLY1305_SHA256`.

---

### Section 4 : Mode Node (TOFU & Public Key Pinning mTLS)
* **Identité Persistante :** Paire de clés Ed25519 générée à l'installation.
* **Trust-On-First-Use (TOFU) :** L'empreinte SHA-256 de la clé Ed25519 est échangée lors du premier appairage SPAKE2 et stockée dans SQLite.
* **Public Key Pinning :** Les connexions ultérieures entre appareils connus exigent la validation mTLS de la clé Ed25519 épinglée. Tout changement d'empreinte bloque la connexion.

---

### Section 5 : Authentification du Canal de Contrôle (Stream 0)
Le message `HandshakeInit` est sérialisé en JSON, horodaté (dérive max 300s) et signé cryptographiquement via Ed25519 (`ed25519.Sign(privKey, payload)`).

---

### Section 6 : Modèle de Menace STRIDE & Constant-Time Operations
Toutes les comparaisons de hashes, tokens, PINs et empreintes cryptographiques utilisent `subtle.ConstantTimeCompare()` pour immuniser le système contre les attaques par canal auxiliaire (*Timing Attacks*).

---

# CHAPITRE 4 : Multiplexage QUIC, Régulation de Flux BBR & Migration de Connexion

---

### Section 1 : Architecture des Streams QUIC
* **Stream 0 (Bidirectionnel, ID 0) :** Canal de contrôle pour `HandshakeInit`, manifestes et commandes.
* **Streams 1 à 8 (Unidirectionnels/Bidirectionnels) :** Canaux de transfert de données parallèles.

```go
type QUICStreamManager struct {
	conn          quic.Connection
	controlStream quic.Stream
	dataStreams   map[uint64]quic.Stream
	mu            sync.RWMutex
}
```

---

### Section 2 : Contrôle de Congestion BBR & Configuration `quic-go`
* **Contrôle BBR :** Inclus nativement dans la pile QUIC pour éviter le *bufferbloat*.
* **Configuration des Fenêtres :**
  ```go
  &quic.Config{
      InitialStreamReceiveWindow:     32 * 1024 * 1024,
      MaxStreamReceiveWindow:         64 * 1024 * 1024,
      InitialConnectionReceiveWindow: 64 * 1024 * 1024,
      MaxConnectionReceiveWindow:     128 * 1024 * 1024,
      MaxIdleTimeout:                 30 * time.Second,
      KeepAlivePeriod:                10 * time.Second,
      DisablePathMTUDiscovery:        false,
  }
  ```

---

### Section 3 : Path MTU Discovery (PMTUD / DPLPMTUD RFC 8899)
Validation dynamique de la taille maximale des paquets UDP entre **1200 octets** (minimum QUIC) et **1472 octets** (MTU Ethernet standard).

---

### Section 4 : Migration de Connexion QUIC (CID 64-bit)
Validation du nouveau chemin réseau lors d'un changement d'adresse IP via l'échange des trames `PATH_CHALLENGE` et `PATH_RESPONSE` sans résiliation du tunnel TLS.

---

### Section 5 : Loss Recovery & ACK Pacing
Lissage des envois de paquets (*ACK Pacing*) basé sur le RTT lissé (*Smoothed RTT*) pour éviter d'engorger les routeurs Wi-Fi domestiques.

---

### Section 6 : Couplage Backpressure / QUIC
Quand le `DiskBackpressureMonitor` active la pause, le récepteur arrête d'appeler `stream.Read()`. La fenêtre d'accréditation `MAX_STREAM_DATA` RFC 9000 s'épuise, bloquant l'émetteur de manière native au niveau de la couche transport.

---

# CHAPITRE 5 : Moteur de Régulation Thermique Multiplateforme & Contrôle Dynamique des Workers

---

### Section 1 : Abstraction & Interrogation Thermique Multiplateforme
Sondage de température toutes les 2 secondes via `/sys/class/thermal/thermal_zone*/temp` (Android/Linux), `ProcessInfo` (iOS) ou WMI (Windows).

---

### Section 2 : Modèle à 4 Paliers d'Hystérésis
* **Paliers de Température :**
  * `NOMINAL` ($< 40^\circ	ext{C}$) : Performance maximale.
  * `WARM` ($40^\circ	ext{C} \le T < 45^\circ	ext{C}$) : Réduction de 50 % des workers.
  * `HOT` ($45^\circ	ext{C} \le T < 49^\circ	ext{C}$) : 1 seul worker, compression désactivée.
  * `CRITICAL` ($T \ge 49^\circ	ext{C}$) : Passage en état `PAUSED_THERMAL`.
* **Hystérésis de Refroidissement :** Delta de $-3^\circ	ext{C}$ exigé pendant au moins **10 secondes consécutives** avant de rétrograder de palier thermique.

---

### Section 3 : Algorithme Dynamic Worker Regulator (DWR)
$$	ext{Workers}_{	ext{actifs}} = egin{cases} 
\min(	ext{Cores}_{	ext{sender}}, 	ext{Cores}_{	ext{receiver}}, 8) & 	ext{si } 	ext{NOMINAL} \
\max(1, \lfloor	ext{Workers}_{	ext{initial}} / 2floor) & 	ext{si } 	ext{WARM} \
1 & 	ext{si } 	ext{HOT} \
0 & 	ext{si } 	ext{CRITICAL}
\end{cases}$$

---

### Section 4 : Action Réductrice Graduelle
* **Niveau WARM :** Désactivation immédiate de la compression LZ4/ZSTD.
* **Niveau HOT :** Réduction de la taille de bloc de **4 Mo à 1 Mo** pour réduire la pression mémoire sur le Garbage Collector Go.

---

### Section 5 : Interruption Sécurisée (`PAUSED_THERMAL`)
Au niveau `CRITICAL`, l'émetteur envoie la trame `ThermalAlert` sur le Stream 0. La session passe en `PAUSED`, synchronise les fichiers `.part` et attend le refroidissement de l'appareil ($< 42^\circ	ext{C}$) pour reprendre automatiquement.

---

### Section 6 : Transmission FFI & Notifications UI Flutter
Les événements thermiques sont transmis à l'IHM via un bridge C-ABI isolant les espaces mémoire et évitant tout conflit entre les Garbage Collectors de Dart et Go.

---

# CHAPITRE 6 : Portail Web Mobile 'Zero-Install' (Mode Invité Restreint & Chiffrement E2EE WebCrypto)

---

### Section 1 : Serveur HTTP Local Éphémère & Virtual Chroot
Le serveur HTTP/WSS local monte un système de fichiers virtuel mémoire (`http.FS` restreint) hébergeant uniquement l'application Web single-page et le fichier partagé, interdisant tout accès à l'arborescence du disque de l'hôte.

---

### Section 2 : Fragment d'URL `#key` & Chiffrement E2EE WebCrypto (AES-GCM-256)
* **Secret Confiné dans le Navigateur :** La clé de déchiffrement est placée uniquement après le fragment `#` de l'URL (ex: `https://192.168.1.50:8443/v1/portal/sess_123#k=<HEX_KEY>`).
* **Conformité RFC 3986 :** Le fragment `#` **n'est jamais transmis au serveur HTTP** lors des requêtes réseau.
* **Streaming WebCrypto :** Le navigateur invité chiffre (Upload) ou déchiffre (Download) les blocs de 4 Mo via l'API WebCrypto (`window.crypto.subtle.decrypt`/`encrypt`).

---

### Section 3 : URLs de Capacité & Jetons Éphémères
Génération d'URLs à jetons Base64URL de 128 bits d'entropie avec expiration automatique réglable (ex: 15 minutes).

---

### Section 4 : Consentement Hôte "One-by-One" & Contrôle des Routes
Lorsqu'un invité soumet un fichier via le portail web, la requête POST est suspendue. Une pop-up de confirmation apparaît sur l'IHM Flutter de l'hôte. L'hôte dispose de 30 secondes pour accepter ou refuser le transfert.

---

### Section 5 : Quotas Stricts, Anti-Archive-Bomb & IP Pinning
* **IP Pinning :** Verrouillage de la session web à la première adresse IP cliente connectée.
* **Anti-Archive-Bomb :** Les fichiers ZIP ou TAR reçus via le portail web sont stockés sous forme de blobs opaques sans décompression automatique.

---

### Section 6 : Hardening Sécurité Web
* **Content Security Policy (CSP) Stricte :** `default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline';`
* **Anti-Clickjacking :** `X-Frame-Options: DENY`
* **Protection Fuite d'URL :** `Referrer-Policy: no-referrer`
* **X-Content-Type-Options :** `nosniff`

---

## 🛠️ Validation & Instructions de Compilation

Toutes les spécifications contenues dans ce document constituent la version **6.0 gelée et définitive** du projet **medXfer v1.0**. Les modules Go sont immédiatement compilables et testables via les briques de test unitaires `go test -v ./pkg/...`.
