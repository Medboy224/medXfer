/// What the developer panel shows and runs (WP-B.5, DEV-19-R): the live telemetry of the
/// transfer, the disk and network benchmarks, the last transfer's report, and the diagnostic
/// bundle. Everything is asked of the core; nothing here measures by itself.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../core/core.dart';

/// The transfer as the core measures it, at its last progress event.
@immutable
class Telemetry {
  const Telemetry({
    required this.speedMBps,
    required this.bytes,
    required this.totalBytes,
    this.stages = const {},
    this.bottleneck = '',
    this.confidence = '',
  });

  factory Telemetry.fromJson(Map<String, Object?> m) => Telemetry(
    speedMBps: (m['speed_mbps'] as num?)?.toDouble() ?? 0,
    bytes: (m['batch_bytes'] as num?)?.toInt() ?? 0,
    totalBytes: (m['batch_total_bytes'] as num?)?.toInt() ?? 0,
    stages: {
      for (final e in ((m['stage_ms'] as Map?) ?? const {}).entries)
        if (e.value is num) '${e.key}': (e.value as num).toDouble(),
    },
    bottleneck: (m['bottleneck'] as String?) ?? '',
    confidence: (m['bottleneck_confidence'] as String?) ?? '',
  );

  final double speedMBps;
  final int bytes;
  final int totalBytes;

  /// Each stage's time so far, divided by how many may run at once, in ms (DEV-03).
  final Map<String, double> stages;

  /// The limiting stage, and how clearly it stands out: high, medium or low (DEV-04).
  final String bottleneck;
  final String confidence;
}

/// The disk benchmark: writing then reading back a file in the download folder, synced.
@immutable
class DiskBench {
  const DiskBench({required this.writeMBps, required this.readMBps, required this.rating, this.warning = ''});

  factory DiskBench.fromJson(Map<String, Object?> m) => DiskBench(
    writeMBps: (m['write_speed_mbps'] as num?)?.toDouble() ?? 0,
    readMBps: (m['read_speed_mbps'] as num?)?.toDouble() ?? 0,
    rating: (m['rating'] as String?) ?? '',
    warning: (m['warning'] as String?) ?? '',
  );

  final double writeMBps;
  final double readMBps;
  final String rating;
  final String warning;
}

/// The network benchmark with the connected device: round trips, then a burst.
@immutable
class NetBench {
  const NetBench({required this.throughputMBps, required this.avgRttMs, required this.minRttMs, required this.maxRttMs, required this.linkType});

  factory NetBench.fromJson(Map<String, Object?> m) => NetBench(
    throughputMBps: (m['throughput_mbps'] as num?)?.toDouble() ?? 0,
    avgRttMs: (m['avg_rtt_ms'] as num?)?.toDouble() ?? 0,
    minRttMs: (m['min_rtt_ms'] as num?)?.toDouble() ?? 0,
    maxRttMs: (m['max_rtt_ms'] as num?)?.toDouble() ?? 0,
    linkType: (m['link_type'] as String?) ?? '',
  );

  final double throughputMBps;
  final double avgRttMs;
  final double minRttMs;
  final double maxRttMs;
  final String linkType;
}

enum DevJob { disk, network, report, bundle }

/// What the core answered instead of a result.
class DevError implements Exception {
  const DevError(this.message);

  final String message;

  @override
  String toString() => message;
}

class DevTools extends ChangeNotifier {
  DevTools(this.core, {required this.downloadDir}) {
    _events = core.events.listen(_on);
  }

  final Core core;

  /// Where the disk benchmark writes and the bundle goes: the folder received files go to.
  final Future<String> Function() downloadDir;

  /// The speeds kept for the curve: about the last 30 seconds (the core sends up to 10 a second).
  static const samples = 300;

  /// The disk benchmark's file: big enough to go past the caches' first burst.
  static const diskBenchBytes = 64 << 20;

  final _speeds = <double>[];
  Telemetry? _live;
  DiskBench? _disk;
  NetBench? _net;
  String? _report;
  String? _bundle;
  final _running = <DevJob>{};
  final _errors = <DevJob, String>{};
  late final StreamSubscription<CoreEvent> _events;

  /// The last transfer's speeds, oldest first.
  List<double> get speeds => List.unmodifiable(_speeds);
  Telemetry? get live => _live;
  DiskBench? get disk => _disk;
  NetBench? get net => _net;

  /// The last transfer's report as text, once asked; empty when there is none yet.
  String? get report => _report;

  /// The bundle written last.
  String? get bundle => _bundle;

  bool running(DevJob j) => _running.contains(j);
  String? error(DevJob j) => _errors[j];

  void _on(CoreEvent e) {
    if (e.name != 'transfer_progress') return;
    final t = Telemetry.fromJson(e.map);
    final last = _live;
    // Another transfer: its own curve.
    if (last != null && (t.totalBytes != last.totalBytes || t.bytes < last.bytes)) _speeds.clear();
    _live = t;
    _speeds.add(t.speedMBps);
    if (_speeds.length > samples) _speeds.removeRange(0, _speeds.length - samples);
    notifyListeners();
  }

  Future<void> runDisk() => _run(DevJob.disk, () async {
    final m = await _ask('test_disk', {'dir': await downloadDir(), 'size_bytes': diskBenchBytes}, 'benchmark_disk_result');
    _disk = DiskBench.fromJson(m);
  });

  /// Needs a connected device: the core says so otherwise.
  Future<void> runNetwork() => _run(DevJob.network, () async {
    _net = NetBench.fromJson(await _ask('test_network', const {}, 'benchmark_network_result'));
  });

  Future<void> loadReport() => _run(DevJob.report, () async {
    final m = await _ask('get_transfer_summary', const {}, 'last_transfer_summary');
    _report = (m['formatted_report'] as String?) ?? '';
  });

  /// The diagnostic bundle (DEV-11): the reports, the journal, the configuration, without
  /// secrets nor file names.
  Future<void> exportBundle() => _run(DevJob.bundle, () async {
    final m = await _ask('export_bundle', {'dir': await downloadDir()}, 'bundle_exported');
    _bundle = m['path'] as String?;
  });

  Future<void> _run(DevJob job, Future<void> Function() work) async {
    if (!_running.add(job)) return;
    _errors.remove(job);
    notifyListeners();
    try {
      await work();
    } on Object catch (e) {
      _errors[job] = e is TimeoutException ? 'timeout' : '$e';
    } finally {
      _running.remove(job);
      notifyListeners();
    }
  }

  /// Runs action and gives the data of its answer, named answer: in the call's replies, or
  /// later on the events with the call's id (a benchmark answers from a goroutine).
  Future<Map<String, Object?>> _ask(String action, Map<String, Object?> payload, String answer) async {
    bool fits(CoreEvent e) => e.name == answer || e.name == 'action_error';
    String? id;
    final early = <CoreEvent>[];
    final later = Completer<CoreEvent>();
    // Listening before the call: an answer may come before the call returns.
    final sub = core.events.listen((e) {
      if (id == null) {
        early.add(e);
      } else if (e.id == id && fits(e) && !later.isCompleted) {
        later.complete(e);
      }
    });
    try {
      final r = await core.call(action, payload);
      if (!r.ok) throw DevError(r.error ?? action);
      id = r.id;
      final e = r.events.where(fits).firstOrNull ??
          early.where((e) => e.id == id && fits(e)).firstOrNull ??
          await later.future.timeout(const Duration(minutes: 2));
      if (e.name == 'action_error') throw DevError((e.map['error'] as String?) ?? action);
      return e.map;
    } finally {
      await sub.cancel();
    }
  }

  @override
  void dispose() {
    unawaited(_events.cancel());
    super.dispose();
  }
}
