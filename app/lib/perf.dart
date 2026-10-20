/// Frame timings in profile builds (UI-1): every two seconds of activity, the frames drawn, their
/// rate, and the slowest build and raster times, in the log (logcat). Debug builds run the Dart
/// code without compiling it ahead: their figures say nothing of the app as shipped.
library;

import 'dart:ui';

import 'package:flutter/foundation.dart';
import 'package:flutter/scheduler.dart';

class FrameStats {
  static final _timings = <FrameTiming>[];
  static int _windowStart = 0;

  static void start() {
    if (!kProfileMode) return;
    SchedulerBinding.instance.addTimingsCallback(_take);
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
    int p(List<int> v, double q) => v[((v.length - 1) * q).round()];
    final seconds = (last - _windowStart) / 1e6;
    final slow = total.where((us) => us > 16667).length;
    debugPrint('perf: ${_timings.length} frames in ${seconds.toStringAsFixed(1)} s '
        '(${(_timings.length / seconds).toStringAsFixed(0)}/s while drawing), '
        'over 16.7 ms: $slow, build p90 ${(p(build, .9) / 1000).toStringAsFixed(1)} ms, '
        'raster p90 ${(p(raster, .9) / 1000).toStringAsFixed(1)} ms, total p90 ${(p(total, .9) / 1000).toStringAsFixed(1)} ms');
    _timings.clear();
    _windowStart = 0;
  }
}
