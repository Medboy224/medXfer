// S4: transfers with the real engine (plan §6.1 G2A-06, G2A-07).
import 'dart:async';
import 'dart:io';

import 'core.dart';
import 'tests.dart';

/// Runs transfers through the core and waits for their events.
class Transfers {
  Transfers(this.core, this.log);
  final Core core;
  final Log log;
  late final CallbackSource _src = CallbackSource(core);
  final _done = <String, Completer<Map<String, dynamic>>>{};
  StreamSubscription<Map<String, dynamic>>? _sub;

  Future<void> open() async {
    await _src.start();
    _sub = _src.events.listen((e) {
      final data = e['data'];
      if (data is! Map) return;
      final id = data['transfer_id'];
      if (e['event'] == 'transfer_done' || e['event'] == 'transfer_error') {
        _done.putIfAbsent(id, Completer.new);
        if (!_done[id]!.isCompleted) _done[id]!.complete({'event': e['event'], ...data.cast<String, dynamic>()});
      }
    });
  }

  Future<void> close() async {
    await _sub?.cancel();
    await _src.stop();
  }

  Future<Map<String, dynamic>> wait(String id, Duration timeout) =>
      _done.putIfAbsent(id, Completer.new).future.timeout(timeout);

  Map<String, dynamic> ok(String action, Map<String, dynamic> payload) {
    final r = core.call(action, payload);
    if (r['ok'] != true) throw StateError('$action: ${r['error']}');
    return r;
  }

  String sha(String path) => ok('sha256', {'path': path})['sha256'] as String;

  /// G2A-06: loopback transfer in the app, hash checked, throughput per trial.
  Future<Map<String, dynamic>> loopback({required int sizeMB, int trials = 3}) async {
    final base = '${Directory.systemTemp.path}${Platform.pathSeparator}g2a_xfer';
    final src = '$base${Platform.pathSeparator}src${Platform.pathSeparator}loop.bin';
    final out = '$base${Platform.pathSeparator}out';
    ok('remove', {'path': base});
    Directory('$base${Platform.pathSeparator}src').createSync(recursive: true);
    final made = ok('make_file', {'path': src, 'size_mb': sizeMB});
    log('test file $sizeMB MiB written in ${made['ms']} ms');
    final want = sha(src);
    final runs = <Map<String, dynamic>>[];
    for (var i = 1; i <= trials; i++) {
      ok('remove', {'path': out});
      final port = 19990 + i;
      final s = ok('send_file', {'path': src, 'port': port});
      final r = ok('receive_file', {'addr': '127.0.0.1:$port', 'out_dir': out});
      final sw = Stopwatch()..start();
      final recv = await wait(r['transfer_id'] as String, const Duration(minutes: 10));
      final secs = sw.elapsedMilliseconds / 1000;
      final send = await wait(s['transfer_id'] as String, const Duration(seconds: 30));
      final got = recv['event'] == 'transfer_done' ? sha(recv['path'] as String) : null;
      final run = {
        'trial': i,
        'recv_event': recv['event'],
        'send_event': send['event'],
        if (recv['error'] != null) 'error': recv['error'],
        'seconds_dart': secs,
        'engine_ms': recv['engine_ms'],
        'mib_per_s': sizeMB / secs,
        'hash_ok': got == want,
      };
      log('loopback $i: $run');
      runs.add(run);
    }
    ok('remove', {'path': base});
    final speeds = [for (final r in runs) r['mib_per_s'] as double]..sort();
    return {
      'pass': runs.every((r) => r['hash_ok'] == true),
      'note': 'throughput criterion (>= 95 % of the CLI) is checked against xfer send/recv on the same device',
      'size_mib': sizeMB,
      'sha256': want,
      'median_mib_per_s': speeds[speeds.length ~/ 2],
      'runs': runs,
    };
  }

  /// G2A-07: the app sends a file to `xfer recv`, then receives one from `xfer send` at [peer].
  Future<Map<String, dynamic>> betweenDevices({required int sizeMB, required String peer}) async {
    final net = ok('get_network', {});
    log('network: $net');
    final base = '${Directory.systemTemp.path}${Platform.pathSeparator}g2a_xfer';
    final src = '$base${Platform.pathSeparator}src${Platform.pathSeparator}from_app.bin';
    final out = '$base${Platform.pathSeparator}out';
    ok('remove', {'path': base});
    Directory('$base${Platform.pathSeparator}src').createSync(recursive: true);
    ok('make_file', {'path': src, 'size_mb': sizeMB});
    final want = sha(src);
    // Phase 1: app -> CLI. The PC runs: xfer recv -ip <app ip>:19990
    final s = ok('send_file', {'path': src, 'port': 19990});
    log('G2A07_SENDING port=19990 code=${s['pairing_code']} sha256=$want');
    final sw1 = Stopwatch()..start();
    final sent = await wait(s['transfer_id'] as String, const Duration(minutes: 15));
    final sendSecs = sw1.elapsedMilliseconds / 1000;
    log('G2A07_SENT $sent');
    ok('remove', {'path': src});

    // Phase 2: CLI -> app. The PC runs: xfer send <file> (port 18888); the app retries until it is up.
    log('G2A07_WAITING_FOR $peer');
    Map<String, dynamic>? recv;
    final sw2 = Stopwatch();
    final deadline = DateTime.now().add(const Duration(minutes: 10));
    final host = peer.substring(0, peer.lastIndexOf(':'));
    final port = int.parse(peer.substring(peer.lastIndexOf(':') + 1));
    while (DateTime.now().isBefore(deadline)) {
      // Receiver.Pull dials without a timeout: towards a firewall that drops packets to a closed
      // port, each attempt hangs for minutes. Wait until the sender's port answers.
      try {
        (await Socket.connect(host, port, timeout: const Duration(seconds: 2))).destroy();
      } catch (_) {
        await Future<void>.delayed(const Duration(seconds: 2));
        continue;
      }
      final r = ok('receive_file', {'addr': peer, 'out_dir': out});
      sw2
        ..reset()
        ..start();
      recv = await wait(r['transfer_id'] as String, const Duration(minutes: 15));
      if (recv['event'] == 'transfer_done') break;
      await Future<void>.delayed(const Duration(seconds: 2));
    }
    final recvSecs = sw2.elapsedMilliseconds / 1000;
    final gotSha = recv?['event'] == 'transfer_done' ? sha(recv!['path'] as String) : null;
    log('G2A07_RECEIVED sha256=$gotSha');
    ok('remove', {'path': base});
    return {
      'pass': sent['event'] == 'transfer_done' && recv?['event'] == 'transfer_done',
      'note': 'hashes are compared with the PC side (sha256 of the file received / sent by xfer)',
      'network': net,
      'size_mib': sizeMB,
      'app_to_cli': {'event': sent['event'], 'sha256': want, 'seconds': sendSecs, 'mib_per_s': sizeMB / sendSecs},
      'cli_to_app': {'event': recv?['event'], 'error': recv?['error'], 'sha256': gotSha, 'seconds': recvSecs,
        'mib_per_s': sizeMB / recvSecs},
    };
  }
}
