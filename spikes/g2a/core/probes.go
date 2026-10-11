package main

// S5: Android probes (plan §6.2). net_probe answers Q6 / G2A-09 (what the Go network stack can
// do inside an app); storage_probe and copy_file answer Q7 / G2A-10 (where the core can write).
// They report every error as text instead of failing, so one run shows the whole picture.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Medboy224/medXfer/pkg/discovery"
)

func errText(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func init() {
	// net_probe {listen_s}: interface enumeration, TCP and UDP listening, multicast join and
	// self-reception, broadcast send; then, for listen_s seconds, counts the UDP packets received
	// from other hosts on the multicast group and on the discovery port (sent by the PC).
	handlers["net_probe"] = func(_ *coreState, p json.RawMessage) (map[string]any, error) {
		var args struct {
			ListenS int `json:"listen_s"`
		}
		_ = json.Unmarshal(p, &args)
		res := map[string]any{}

		ifs, err := net.Interfaces()
		var ifNames []string
		for _, ifc := range ifs {
			addrs, aerr := ifc.Addrs()
			ifNames = append(ifNames, fmt.Sprintf("%s flags=%s addrs=%v err=%v", ifc.Name, ifc.Flags, addrs, aerr))
		}
		res["interfaces"] = map[string]any{"count": len(ifs), "list": ifNames, "error": errText(err)}

		addrs, err := net.InterfaceAddrs()
		var addrList []string
		for _, a := range addrs {
			addrList = append(addrList, a.String())
		}
		res["interface_addrs"] = map[string]any{"list": addrList, "error": errText(err)}

		var targets []string
		for _, t := range discovery.GetActiveNetworkTargets() {
			targets = append(targets, fmt.Sprintf("%s local=%s bcast=%s", t.InterfaceName, t.LocalIP, t.BroadcastIP))
		}
		res["discovery_targets"] = targets
		res["discovery_primary_ip"] = discovery.GetPrimaryLocalIP()

		if l, err := net.Listen("tcp4", "0.0.0.0:0"); err == nil {
			res["tcp_listen_any"] = map[string]any{"ok": true, "addr": l.Addr().String()}
			l.Close()
		} else {
			res["tcp_listen_any"] = map[string]any{"ok": false, "error": err.Error()}
		}

		group := &net.UDPAddr{IP: net.ParseIP(discovery.MulticastGroup), Port: 19997}
		mc, err := net.ListenMulticastUDP("udp4", nil, group)
		res["multicast_join"] = map[string]any{"ok": err == nil, "group": group.String(), "error": errText(err)}
		udp, uerr := net.ListenUDP("udp4", &net.UDPAddr{Port: discovery.DiscoveryPort})
		res["udp_listen_discovery_port"] = map[string]any{"ok": uerr == nil, "error": errText(uerr)}

		// Self-reception: a packet sent to the group must come back (IP_MULTICAST_LOOP).
		if mc != nil {
			if s, err := net.DialUDP("udp4", nil, group); err == nil {
				_, werr := s.Write([]byte("g2a-self"))
				s.Close()
				buf := make([]byte, 64)
				_ = mc.SetReadDeadline(time.Now().Add(time.Second))
				n, from, rerr := mc.ReadFromUDP(buf)
				res["multicast_self_receive"] = map[string]any{
					"ok": rerr == nil && string(buf[:n]) == "g2a-self", "from": fmt.Sprint(from),
					"write_error": errText(werr), "read_error": errText(rerr),
				}
			} else {
				res["multicast_self_receive"] = map[string]any{"ok": false, "error": err.Error()}
			}
		}

		bc, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4bcast, Port: discovery.DiscoveryPort})
		if err == nil {
			_, err = bc.Write([]byte("g2a-bcast"))
			bc.Close()
		}
		res["broadcast_send"] = map[string]any{"ok": err == nil, "error": errText(err)}

		// Reception from other hosts (the PC sends to the group and to the subnet broadcast).
		if args.ListenS > 0 {
			self := map[string]bool{}
			for _, a := range addrList {
				self[strings.SplitN(a, "/", 2)[0]] = true
			}
			if ip := discovery.GetPrimaryLocalIP(); ip != "" {
				self[ip] = true
			}
			count := func(c *net.UDPConn) (int, []string) {
				n, seen := 0, map[string]bool{}
				buf := make([]byte, 1500)
				deadline := time.Now().Add(time.Duration(args.ListenS) * time.Second)
				for time.Now().Before(deadline) {
					_ = c.SetReadDeadline(deadline)
					_, from, err := c.ReadFromUDP(buf)
					if err != nil {
						break
					}
					if !self[from.IP.String()] {
						n++
						seen[from.IP.String()] = true
					}
				}
				var srcs []string
				for s := range seen {
					srcs = append(srcs, s)
				}
				return n, srcs
			}
			var wg sync.WaitGroup
			var mN, bN int
			var mFrom, bFrom []string
			if mc != nil {
				wg.Add(1)
				go func() { defer wg.Done(); mN, mFrom = count(mc) }()
			}
			if udp != nil {
				wg.Add(1)
				go func() { defer wg.Done(); bN, bFrom = count(udp) }()
			}
			wg.Wait()
			res["received_from_others"] = map[string]any{
				"seconds": args.ListenS, "multicast_packets": mN, "multicast_from": mFrom,
				"discovery_port_packets": bN, "discovery_port_from": bFrom,
			}
		}
		if mc != nil {
			mc.Close()
		}
		if udp != nil {
			udp.Close()
		}
		return res, nil
	}

	// storage_probe {dirs: [{name, path}], size_mb}: in each directory, the steps the receiver
	// performs today: create a sub-directory, write a .part file, fsync, rename it to its final
	// name, read it back, delete it.
	handlers["storage_probe"] = func(_ *coreState, p json.RawMessage) (map[string]any, error) {
		var args struct {
			Dirs []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"dirs"`
			SizeMB int `json:"size_mb"`
		}
		if err := json.Unmarshal(p, &args); err != nil || len(args.Dirs) == 0 {
			return nil, fmt.Errorf("payload must be {dirs: [{name, path}], size_mb?}")
		}
		if args.SizeMB <= 0 {
			args.SizeMB = 8
		}
		res := map[string]any{}
		for _, d := range args.Dirs {
			res[d.Name] = probeDir(d.Path, args.SizeMB)
		}
		return res, nil
	}

	// copy_file {src, dst}: plain copy with fsync, to cost the "receive privately, then copy to
	// the public folder" approach when the public folder is reachable by path.
	handlers["copy_file"] = func(_ *coreState, p json.RawMessage) (map[string]any, error) {
		var args struct {
			Src string `json:"src"`
			Dst string `json:"dst"`
		}
		if err := json.Unmarshal(p, &args); err != nil || args.Src == "" || args.Dst == "" {
			return nil, fmt.Errorf("payload must be {src, dst}")
		}
		start := time.Now()
		in, err := os.Open(args.Src)
		if err != nil {
			return nil, err
		}
		defer in.Close()
		out, err := os.Create(args.Dst)
		if err != nil {
			return nil, err
		}
		n, err := io.CopyBuffer(out, in, make([]byte, 4<<20))
		if err == nil {
			err = out.Sync()
		}
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"bytes": n, "ms": time.Since(start).Milliseconds()}, nil
	}
}

func probeDir(dir string, sizeMB int) map[string]any {
	r := map[string]any{"path": dir}
	step := func(name string, err error) bool {
		r[name] = errText(err)
		if err == nil {
			r[name] = "ok"
		}
		return err == nil
	}
	sub := filepath.Join(dir, "medxfer_probe")
	if !step("mkdir", os.MkdirAll(sub, 0o755)) {
		return r
	}
	defer os.RemoveAll(sub)
	part := filepath.Join(sub, "probe.bin.part")
	final := filepath.Join(sub, "probe.bin")
	start := time.Now()
	f, err := os.Create(part)
	if !step("create", err) {
		return r
	}
	buf := make([]byte, 1<<20)
	for i := range buf {
		buf[i] = byte(i)
	}
	var werr error
	for i := 0; i < sizeMB && werr == nil; i++ {
		_, werr = f.Write(buf)
	}
	step("write", werr)
	step("fsync", f.Sync())
	step("close", f.Close())
	r["write_ms"] = time.Since(start).Milliseconds()
	if !step("rename", os.Rename(part, final)) {
		return r
	}
	got, err := os.ReadFile(final)
	if step("read", err) {
		r["read_size_ok"] = len(got) == sizeMB<<20
	}
	step("remove", os.Remove(final))
	return r
}
