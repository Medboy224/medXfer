// S5: robustness and Android probes (plan §6.1 G2A-05, §6.2 G2A-09 and G2A-10).
import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';

import 'core.dart';
import 'tests.dart';
import 'transfers.dart';

const _platform = MethodChannel('g2a/platform');

class S5 {
  S5(this.core, this.log);
  final Core core;
  final Log log;

  Map<String, dynamic> _ok(String action, [Map<String, dynamic>? payload]) {
    final r = core.call(action, payload);
    if (r['ok'] != true) throw StateError('$action: ${r['error']}');
    return r;
  }

  /// G2A-09 and G2A-10 on Android. The PC sends UDP packets while the probe listens (between
  /// the G2A09_LISTEN and G2A09_DONE lines), once without and once with the multicast lock.
  Future<Map<String, dynamic>> androidProbes({required int sizeMB, int listenS = 8}) async {
    final paths = Map<String, dynamic>.from(await _platform.invokeMethod('paths') as Map);
    log('paths $paths');
    final res = <String, dynamic>{'platform_paths': paths};

    for (final lock in [false, true]) {
      final held = await _platform.invokeMethod('multicastLock', {'acquire': lock});
      log('G2A09_LISTEN lock=$lock held=$held seconds=$listenS');
      await Future<void>.delayed(const Duration(milliseconds: 300));
      res['net_probe_lock_$lock'] = _ok('net_probe', {'listen_s': listenS});
      log('G2A09_DONE lock=$lock ${res['net_probe_lock_$lock']['received_from_others']}');
    }
    await _platform.invokeMethod('multicastLock', {'acquire': false});

    final downloads = paths['downloads_public'] as String;
    final dirs = [
      {'name': 'dart_system_temp', 'path': Directory.systemTemp.path},
      {'name': 'files', 'path': paths['files']},
      {'name': 'cache', 'path': paths['cache']},
      {'name': 'external_files', 'path': paths['external_files']},
      {'name': 'downloads_direct', 'path': downloads},
      {'name': 'sdcard_download', 'path': '/sdcard/Download'},
    ];
    final storage = _ok('storage_probe', {'dirs': dirs, 'size_mb': 8});
    res['storage_probe'] = storage;
    log('storage $storage');

    // Cost of "receive privately, then publish": baseline write of the file in the private
    // directory, then the copy to Download through MediaStore and, if allowed, by path.
    final priv = '${paths['files']}/g2a_cost.bin';
    final made = _ok('make_file', {'path': priv, 'size_mb': sizeMB});
    final cost = <String, dynamic>{'size_mib': sizeMB, 'private_write_ms': made['ms']};
    try {
      final r = Map<String, dynamic>.from(
          await _platform.invokeMethod('copyToDownloads', {'src': priv, 'name': 'g2a_cost.bin'}) as Map);
      cost['mediastore_copy_ms'] = r['ms'];
      cost['mediastore_uri'] = r['uri'];
      await _platform.invokeMethod('deleteUri', {'uri': r['uri']});
    } on PlatformException catch (e) {
      cost['mediastore_copy_error'] = '${e.message}';
    }
    if ((storage['downloads_direct'] as Map)['rename'] == 'ok') {
      final r = core.call('copy_file', {'src': priv, 'dst': '$downloads/g2a_cost_direct.bin'});
      cost['direct_copy_ms'] = r['ms'];
      cost['direct_copy_error'] = r['error'];
      core.call('remove', {'path': '$downloads/g2a_cost_direct.bin'});
    }
    core.call('remove', {'path': priv});
    for (final k in ['private_write_ms', 'mediastore_copy_ms', 'direct_copy_ms']) {
      if (cost[k] is int) cost[k.replaceAll('_ms', '_mib_per_s')] = sizeMB * 1000 / (cost[k] as int);
    }
    res['receive_then_publish_cost'] = cost;
    log('cost $cost');
    res['pass'] = true; // informative criteria: the values are the result
    return res;
  }

  /// G2A-05 (hot restart): each generation of the Dart program re-runs main(); the core must
  /// answer already_started (B4) and keep working. Events flow while the restart happens, so a
  /// stale callback or a stuck poll reader would show up.
  Future<void> restartGeneration(Map<String, dynamic> startResult, String model) async {
    log('G2A_RESTART start=$startResult model=$model');
    final EventSource src = model == 'poll' ? PollSource(core) : CallbackSource(core);
    await src.start();
    var n = 0;
    src.events.listen((e) {
      if (e['event'] == 'tick') n++;
    });
    core.call('emit_events', {'count': 1000, 'per_sec': 200}); // 5 s: overlaps the next restart
    Timer.periodic(const Duration(seconds: 1), (_) {
      final m = core.call('memstats');
      log('G2A_RESTART_ALIVE events=$n goroutines=${m['goroutines']} sys_mb=${(m['sys_mb'] as num).toStringAsFixed(1)} '
          'rss_mb=${(ProcessInfo.currentRss / 1048576).toStringAsFixed(0)}');
    });
  }

  /// G2A-05 (Android background, kill): serve a file and wait, so that the app can be sent to the
  /// background, brought back, or killed and relaunched while its TCP listener is open.
  Future<Map<String, dynamic>> listen({required int sizeMB}) async {
    final t = Transfers(core, log);
    await t.open();
    final base = '${Directory.systemTemp.path}/g2a_listen';
    core.call('remove', {'path': base});
    Directory(base).createSync(recursive: true);
    final src = '$base/listen.bin';
    _ok('make_file', {'path': src, 'size_mb': sizeMB});
    final want = t.sha(src);
    final s = _ok('send_file', {'path': src, 'port': 19990});
    log('G2A05_LISTENING port=19990 code=${s['pairing_code']} sha256=$want');
    final done = await t.wait(s['transfer_id'] as String, const Duration(minutes: 30));
    log('G2A05_SENT $done');
    final m = core.call('memstats');
    await t.close();
    core.call('remove', {'path': base});
    return {'pass': done['event'] == 'transfer_done', 'sent': done, 'sha256': want, 'memstats_end': m};
  }
}
