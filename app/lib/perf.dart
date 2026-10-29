/// Frame timings in profile builds (UI-1): every two seconds of activity, the frames drawn, their
/// rate, the slowest build and raster times, and the longest frame, in the log (logcat, or the
/// console on a desktop). Debug builds run the Dart code without compiling it ahead: their
/// figures say nothing of the app as shipped.
///
/// A still screen draws nothing, so a hitch at rest is not seen. With
/// --dart-define=MEDXFER_PERF_TICK=true, a frame is asked at every vsync, without drawing
/// anything new: the stutters of G2a's reserve 4 (Windows, 230 to 240 ms, even without any
/// event) are measured at rest too (WP-C.4 of the J6c plan).
library;

import 'dart:ui';

import 'package:flutter/foundation.dart';
import 'package:flutter/scheduler.dart';

class FrameStats {
  static final _timings = <FrameTiming>[];
  static int _windowStart = 0;

  static const _tick = bool.fromEnvironment('MEDXFER_PERF_TICK');

  static void start() {
    if (!kProfileMode) return;
    SchedulerBinding.instance.addTimingsCallback(_take);
    if (_tick) SchedulerBinding.instance.addPostFrameCallback(_again);
  }

  static void _again(Duration _) {
    SchedulerBinding.instance
      ..addPostFrameCallback(_again)
      ..scheduleFrame();
  }

  static void _take(List<FrameTiming> timings) {
    _timings.addAll(timings);
    final first = _timings.first.timestampInMicroseconds(FramePhase.vsyncStart);
    final last = _timings.last.timestampInMicroseconds(FramePhase.rasterFinish);
    if (_windowStart == 0) _windowStart = first;
    if (last - _windowStart < 2000000) return;
    final build = [for (final t in _timings) t.buildDuration.inMicroseconds]..sort();
    final raster = [for (final t in _timings) t.rasterDuration.inMicroseconds]..sort();
    final total = [for (final t in _timings) t.totalSpan.inMicroseconds]..sort();
    // The gap between two frames: a hitch shows there even when each frame is quick.
    var gap = 0;
    for (var i = 1; i < _timings.length; i++) {
      final d = _timings[i].timestampInMicroseconds(FramePhase.vsyncStart) - _timings[i - 1].timestampInMicroseconds(FramePhase.vsyncStart);
      if (d > gap) gap = d;
    }
    int p(List<int> v, double q) => v[((v.length - 1) * q).round()];
    final seconds = (last - _windowStart) / 1e6;
    final slow = total.where((us) => us > 16667).length;
    final hitches = total.where((us) => us > 100000).length;
    debugPrint('perf: ${_timings.length} frames in ${seconds.toStringAsFixed(1)} s '
        '(${(_timings.length / seconds).toStringAsFixed(0)}/s while drawing), '
        'over 16.7 ms: $slow, over 100 ms: $hitches, build p90 ${(p(build, .9) / 1000).toStringAsFixed(1)} ms, '
        'raster p90 ${(p(raster, .9) / 1000).toStringAsFixed(1)} ms, total p90 ${(p(total, .9) / 1000).toStringAsFixed(1)} ms, '
        'max ${(total.last / 1000).toStringAsFixed(1)} ms, longest gap ${(gap / 1000).toStringAsFixed(1)} ms');
    _timings.clear();
    _windowStart = 0;
  }
}
