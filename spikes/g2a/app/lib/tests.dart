// G2a test suite: each test measures one blocking criterion of plan §6.1 and returns its
// numbers, so the report can be filled from the JSON results.
import 'dart:async';
import 'dart:io';

import 'core.dart';

typedef Log = void Function(String);

class Suite {
  Suite(this.core, this.log, {required this.full, this.only});
  final Core core;
  final String? only; // run only the tests whose id contains this text
  final Log log;
  final bool full;

  Future<Map<String, dynamic>> runAll(Duration sinceMain) async {
    final results = <String, dynamic>{
      'platform': Platform.operatingSystem,
      'os_version': Platform.operatingSystemVersion,
      'dart': Platform.version.split(' ').first,
      'mode': full ? 'full' : 'quick',
      'cold_start_ms_main_to_first_ping': sinceMain.inMicroseconds / 1000, // G2A-12
    };
    Future<void> step(String id, Future<Map<String, dynamic>> Function() f) async {
      if (only != null && !id.contains(only!)) return;
      log('$id …');
      try {
        final r = await f();
        results[id] = r;
        log('$id: ${r['pass'] == true ? 'PASS' : 'FAIL'} $r');
      } catch (e, st) {
        results[id] = {'pass': false, 'exception': '$e', 'stack': '$st'};
        log('$id: EXCEPTION $e');
      }
    }

    await step('G2A-05_lifecycle', lifecycle);
    await step('G2A-02_ping', ping);
    await step('G2A-04_panic', () => panics());
    await step('G2A-03_baseline_no_events', baseline);
    for (final model in ['callback', 'poll']) {
      await step('G2A-03_${model}_steady', () => events(model, steady: true));
      await step('G2A-03_${model}_burst', () => events(model, steady: false));
    }
    results['pass'] = results.values.whereType<Map>().every((r) => r['pass'] == true);
    return results;
  }

  // G2A-05 (part run in-process): Start is idempotent (B4), Start → Stop → Start works.
  Future<Map<String, dynamic>> lifecycle() async {
    final second = core.start({});
    core.stop();
    final afterStop = core.call('ping')['started'];
    final restart = core.start({});
    final afterRestart = core.call('ping')['started'];
    final pass = second['already_started'] == true && afterStop == false &&
        restart['already_started'] == false && afterRestart == true;
    return {
      'pass': pass,
      'second_start_already_started': second['already_started'],
      'started_after_stop': afterStop,
      'started_after_restart': afterRestart,
    };
  }

  // G2A-02: 1,000 round trips without error; latency; memory stable within ±5 % after 10,000.
  // Memory is read after a 10,000-call warm-up, then at checkpoints: a leak keeps growing
  // linearly, a warm-up plateaus.
  Future<Map<String, dynamic>> ping() async {
    const req = '{"id":"p","action":"ping"}';
    var errors = 0;
    final lat = <int>[];
    for (var i = 0; i < 1000; i++) {
      final sw = Stopwatch()..start();
      final r = core.callRaw(req);
      lat.add(sw.elapsedMicroseconds);
      if (!r.contains('"ok":true')) errors++;
    }
    lat.sort();
    Future<int> calls(int n) async {
      for (var i = 0; i < n; i++) {
        if (!core.callRaw(req).contains('"ok":true')) errors++;
        if (i % 1000 == 999) await Future<void>.delayed(Duration.zero); // let the GC run
      }
      await Future<void>.delayed(const Duration(milliseconds: 300));
      return ProcessInfo.currentRss;
    }

    final rssStart = ProcessInfo.currentRss;
    final goStart = core.call('memstats');
    final rss0 = await calls(10000); // warm-up
    final rss1 = await calls(10000);
    final checkpoints = <String, double>{'start': rssStart / 1048576, 'after_warmup_10k': rss0 / 1048576, '+10k': rss1 / 1048576};
    var total = 10000; // calls since the warm-up
    for (final n in full ? [50000, 100000] : [50000]) {
      total += n;
      checkpoints['+${total ~/ 1000}k'] = await calls(n) / 1048576;
    }
    final drift = (rss1 - rss0) * 100 / rss0;
    final median = lat[lat.length ~/ 2];
    return {
      'pass': errors == 0 && drift.abs() <= 5,
      'errors': errors,
      'latency_us': {'median': median, 'p95': lat[lat.length * 95 ~/ 100], 'max': lat.last},
      'latency_alert_5ms': median > 5000,
      'rss_mb_checkpoints': checkpoints,
      'go_memstats_start': goStart,
      'go_memstats_end': core.call('memstats'),
      'rss_drift_pct_10k_after_warmup': drift,
    };
  }

  // G2A-04: a panic in medxfer_call becomes a JSON error; a panic in a core goroutine becomes
  // a core_error event; the app keeps running.
  Future<Map<String, dynamic>> panics() async {
    final r = core.call('panic_call');
    final callOk = r['ok'] == false && '${r['error']}'.contains('panic');
    final src = CallbackSource(core);
    await src.start();
    final got = src.events.firstWhere((e) => e['event'] == 'core_error').timeout(const Duration(seconds: 5));
    core.call('panic_goroutine');
    Map<String, dynamic>? err;
    try {
      err = await got;
    } catch (_) {}
    await src.stop();
    final alive = core.call('ping')['ok'] == true;
    return {
      'pass': callOk && err != null && alive,
      'panic_call_error': r['error'],
      'core_error_received': err != null,
      'core_error_where': err?['data']?['where'],
      'alive_after': alive,
    };
  }

  // Reference for G2A-03: the same UI-lag probe with no event at all. Lag spikes seen here come
  // from the machine (scheduling, GC, other load), not from the bridge.
  Future<Map<String, dynamic>> baseline() async {
    final probe = LagProbe()..start();
    await Future<void>.delayed(Duration(seconds: full ? 60 : 10));
    probe.stop();
    return {'pass': true, 'informative': true, 'seconds': full ? 60 : 10, ...probe.summary()};
  }

  // G2A-03: numbered events, none lost, none out of order, the UI isolate never blocked.
  // steady: 10,000 at 100/s; burst: 1,000/s for 60 s (quick mode: smaller counts).
  Future<Map<String, dynamic>> events(String model, {required bool steady}) async {
    final perSec = steady ? 100 : 1000;
    final count = steady ? (full ? 10000 : 1000) : (full ? 60000 : 5000);
    final EventSource src = model == 'callback' ? CallbackSource(core) : PollSource(core);
    await src.start();

    var received = 0, outOfOrder = 0, duplicates = 0, last = 0;
    final seen = <int>{};
    final done = Completer<void>();
    final sub = src.events.listen((e) {
      if (e['event'] == 'tick') {
        final seq = e['data']['seq'] as int;
        received++;
        if (!seen.add(seq)) duplicates++;
        if (seq < last) outOfOrder++;
        last = seq;
      } else if (e['event'] == 'ticks_done' && !done.isCompleted) {
        done.complete();
      }
    });

    final probe = LagProbe()..start();

    final sw = Stopwatch()..start();
    final r = core.call('emit_events', {'count': count, 'per_sec': perSec});
    var timedOut = false;
    try {
      await done.future.timeout(Duration(seconds: count ~/ perSec + 30));
    } on TimeoutException {
      timedOut = true;
    }
    // Events still in flight behind ticks_done (poll isolate, listener queue).
    await Future<void>.delayed(const Duration(milliseconds: 300));
    probe.stop();
    final lag = probe.summary();
    final maxLagMs = lag['max_ui_lag_ms'] as double;
    await sub.cancel();
    await src.stop();
    final stats = core.call('stats');

    final missing = count - seen.length;
    final missingSeqs = [for (var i = 1; i <= count; i++) if (!seen.contains(i)) i].take(10).toList();
    return {
      'pass': r['ok'] == true && !timedOut && missing == 0 && outOfOrder == 0 && duplicates == 0 && maxLagMs < 100,
      'model': model,
      'count': count,
      'per_sec': perSec,
      'received': received,
      'missing': missing,
      'missing_seqs_first_10': missingSeqs,
      'out_of_order': outOfOrder,
      'duplicates': duplicates,
      'core_drops': stats['drops'],
      'timed_out': timedOut,
      'elapsed_s': sw.elapsedMilliseconds / 1000,
      ...lag,
    };
  }
}

/// Event-loop lag of the UI isolate: a 10 ms timer that fires late means a frozen UI.
class LagProbe {
  final _watch = Stopwatch();
  Timer? _timer;
  int _prev = 0;
  final _lags = <double>[];

  void start() {
    _watch.start();
    _timer = Timer.periodic(const Duration(milliseconds: 10), (_) {
      final now = _watch.elapsedMicroseconds;
      if (_prev != 0) _lags.add((now - _prev) / 1000 - 10);
      _prev = now;
    });
  }

  void stop() => _timer?.cancel();

  Map<String, dynamic> summary() {
    final sorted = [..._lags]..sort();
    double pct(int p) => sorted.isEmpty ? 0 : sorted[(sorted.length - 1) * p ~/ 100];
    return {
      'max_ui_lag_ms': sorted.isEmpty ? 0.0 : sorted.last,
      'p99_ui_lag_ms': pct(99),
      'lag_over_50ms': sorted.where((l) => l > 50).length,
      'lag_over_100ms': sorted.where((l) => l > 100).length,
    };
  }
}
