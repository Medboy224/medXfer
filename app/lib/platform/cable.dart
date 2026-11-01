/// A cable between two PCs (lot « Câble Ethernet entre deux PC », WP-E.2): with its default
/// profile, NetworkManager leaves a direct cable without any IPv4 address (DHCP fails after
/// 45 s, nobody serves it), and the other PC may have no IPv6 at all (probe WP-E.0). The Linux
/// app then gives the card a link-local address itself (decision E1), by a temporary profile
/// of its own; the user's profiles are never changed. Windows gives itself one (169.254.x.x):
/// nothing to do there.
library;

import 'dart:io';

import 'hotspot.dart' show Runner;

/// What the app does for wired cards left without an address.
abstract interface class CableLink {
  /// Looks at the wired cards once: gives a link-local address to a card with a link that the
  /// system left without IPv4, takes it back when the cable is unplugged. The cards the app
  /// gives an address to now.
  Future<Set<String>> check();

  /// Takes back every address the app gave: the app closes.
  Future<void> release();

  /// The wired card that keeps the address of a connection sharing stopped since (Windows,
  /// 192.168.137.1 set by hand): the system then gives it no 169.254 address, and a PC at the
  /// other end of the cable cannot reach this one (real tests of J6c and of 1 November). null
  /// when none does.
  Future<String?> sharingLeft();
}

class NoCableLink implements CableLink {
  const NoCableLink();

  @override
  Future<Set<String>> check() async => const {};

  @override
  Future<void> release() async {}

  @override
  Future<String?> sharingLeft() async => null;
}

/// Windows gives a cable its 169.254 address itself; what can block it is the address of a
/// connection sharing (ICS) left on the card. Said, never changed: removing it needs the
/// administrator, and the sharing may still be wanted (towards a router).
class WindowsCableLink implements CableLink {
  WindowsCableLink({Future<List<(String, List<String>)>> Function()? interfaces}) : _interfaces = interfaces ?? _system;

  /// Each interface's name and IPv4 addresses.
  final Future<List<(String, List<String>)>> Function() _interfaces;

  static Future<List<(String, List<String>)>> _system() async => [
    for (final i in await NetworkInterface.list(type: InternetAddressType.IPv4)) (i.name, [for (final a in i.addresses) a.address]),
  ];

  /// The address Windows's connection sharing puts on the card it shares to.
  static const sharingAddress = '192.168.137.1';

  @override
  Future<Set<String>> check() async => const {};

  @override
  Future<void> release() async {}

  @override
  Future<String?> sharingLeft() async {
    for (final (name, addresses) in await _interfaces()) {
      // The wired cards only: the computer's own network (WP-C.2) may hold it rightly.
      if (!name.toLowerCase().startsWith('ethernet')) continue;
      if (addresses.contains(sharingAddress)) return name;
    }
    return null;
  }
}

/// One wired card as NetworkManager reports it.
class _Wired {
  _Wired(this.device);

  final String device;
  int state = 0; // NetworkManager's device state: 20 no link, 30 disconnected, 40 to 90 activating, 100 connected, 120 failed
  String connection = '';
  bool ipv4 = false;
}

/// NetworkManager, by nmcli (arguments apart, no shell), as for the network the computer
/// creates (WP-C.2).
class LinuxCableLink implements CableLink {
  LinuxCableLink({Runner? run, DateTime Function()? now}) : _run = run ?? Process.run, _now = now ?? DateTime.now;

  final Runner _run;
  final DateTime Function() _now;

  /// Its profiles' names begin so: one per card ("medxfer-cable-enp0s25").
  static const prefix = 'medxfer-cable-';

  /// How long a card with a link stays without IPv4 before the app gives it one:
  /// NetworkManager's DHCP gives up at 45 s by default, and a real network's answers in a few
  /// seconds. The app never takes the place of a DHCP that is only slow.
  static const dhcpWait = Duration(seconds: 50);

  /// After a profile that did not come up, the next try waits that long.
  static const retryAfter = Duration(minutes: 1);

  final _since = <String, DateTime>{}; // a card with a link and no IPv4, since
  final _tried = <String>{}; // a card where NetworkManager tried DHCP meanwhile
  final _failedAt = <String, DateTime>{};
  final _given = <String>{};
  bool _cleaned = false;

  Future<ProcessResult> _nmcli(List<String> args) => _run('nmcli', args);

  @override
  Future<Set<String>> check() async {
    if (!_cleaned) {
      _cleaned = true;
      await _deleteLeftovers(); // a profile left by an app that did not close properly
    }
    final now = _now();
    for (final w in await _wired()) {
      final ours = '$prefix${w.device}';
      if (w.state <= 20) {
        // No link (or unmanaged): the cable is unplugged. The profile goes, NetworkManager's
        // own comes back on the next plug.
        _forget(w.device);
        if (_given.remove(w.device)) await _nmcli(['connection', 'delete', ours]);
        continue;
      }
      if (w.connection == ours) {
        _given.add(w.device);
        continue;
      }
      _given.remove(w.device);
      // An address of the system's, or a profile the user connected without IPv4 (IPv6
      // alone, IPv4 turned off): theirs, left alone.
      if (w.ipv4 || w.state == 100) {
        _forget(w.device);
        continue;
      }
      final since = _since.putIfAbsent(w.device, () => now);
      if (w.state >= 40 && w.state <= 90) _tried.add(w.device);
      final gaveUp = (w.state == 30 || w.state == 120) && _tried.contains(w.device);
      if (!gaveUp && now.difference(since) < dhcpWait) continue;
      final failed = _failedAt[w.device];
      if (failed != null && now.difference(failed) < retryAfter) continue;
      if (await _give(w.device)) {
        _failedAt.remove(w.device);
        _given.add(w.device);
      } else {
        _failedAt[w.device] = now;
      }
    }
    return {..._given};
  }

  @override
  Future<String?> sharingLeft() async => null;

  @override
  Future<void> release() async {
    for (final device in [..._given]) {
      await _nmcli(['connection', 'delete', '$prefix$device']);
    }
    _given.clear();
  }

  void _forget(String device) {
    _since.remove(device);
    _tried.remove(device);
    _failedAt.remove(device);
  }

  /// A profile of the app's for device: link-local IPv4 and IPv6, never started by the system
  /// on its own.
  Future<bool> _give(String device) async {
    final name = '$prefix$device';
    await _nmcli(['connection', 'delete', name]);
    final add = await _nmcli([
      'connection', 'add', 'type', 'ethernet', 'ifname', device, 'con-name', name, //
      'ipv4.method', 'link-local', 'ipv6.method', 'link-local', 'connection.autoconnect', 'no',
    ]);
    if (add.exitCode != 0) return false;
    if ((await _nmcli(['connection', 'up', name])).exitCode == 0) return true;
    await _nmcli(['connection', 'delete', name]);
    return false;
  }

  Future<void> _deleteLeftovers() async {
    final all = await _nmcli(['-t', '-f', 'NAME', 'connection', 'show']);
    for (final line in '${all.stdout}'.split('\n')) {
      final name = line.trim().replaceAll(r'\:', ':');
      if (name.startsWith(prefix)) await _nmcli(['connection', 'delete', name]);
    }
  }

  /// The wired cards, from "nmcli -t device show": one block per device, a blank line apart.
  Future<List<_Wired>> _wired() async {
    final r = await _nmcli(['-t', '-f', 'GENERAL.DEVICE,GENERAL.TYPE,GENERAL.STATE,GENERAL.CONNECTION,IP4.ADDRESS', 'device', 'show']);
    if (r.exitCode != 0) return const [];
    final out = <_Wired>[];
    _Wired? cur;
    var ethernet = false;
    void close() {
      if (cur != null && ethernet) out.add(cur!);
      cur = null;
      ethernet = false;
    }

    for (final line in '${r.stdout}'.split('\n')) {
      final i = line.indexOf(':');
      if (i < 0) continue;
      final key = line.substring(0, i);
      final value = line.substring(i + 1).trim().replaceAll(r'\:', ':');
      if (key == 'GENERAL.DEVICE') {
        close();
        cur = _Wired(value);
      } else if (cur == null) {
        continue;
      } else if (key == 'GENERAL.TYPE') {
        ethernet = value == 'ethernet';
      } else if (key == 'GENERAL.STATE') {
        cur!.state = int.tryParse(value.split(' ').first) ?? 0;
      } else if (key == 'GENERAL.CONNECTION') {
        cur!.connection = value;
      } else if (key.startsWith('IP4.ADDRESS') && value.isNotEmpty) {
        cur!.ipv4 = true;
      }
    }
    close();
    return out;
  }
}

CableLink systemCableLink() {
  if (Platform.isLinux) return LinuxCableLink();
  if (Platform.isWindows) return WindowsCableLink();
  return const NoCableLink();
}
