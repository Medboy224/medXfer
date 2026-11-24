/// Where each video was left (WP-8.2, mockup 24): opened again, it goes on from there, with
/// « Recommencer » at hand. Kept in resume.json beside the settings, the 200 most recent; a file
/// changed since (another size or date) starts over.
library;

import 'dart:async';

import '../platform/library.dart';
import '../settings.dart';

class ResumePoints {
  ResumePoints({this._store});

  final SettingsStore? _store;
  Map<String, Object?>? _points; // key: seconds, the most recent last
  Future<void>? _loading;
  Future<void> _saving = Future.value();

  /// At most this many videos remembered.
  static const kept = 200;

  /// Too near either end to be worth going back to.
  static const margin = Duration(seconds: 10);

  static String _key(MediaEntry m) => '${m.kind.name}:${m.id}:${m.size}:${m.modified}';

  Future<Map<String, Object?>> _all() async {
    final points = _points;
    if (points != null) return points;
    await (_loading ??= () async {
      _points = Map.of(await _store?.load() ?? const {});
    }());
    return _points!;
  }

  /// Where [m] was left; null to start from its beginning.
  Future<Duration?> of(MediaEntry m) async {
    final s = (await _all())[_key(m)];
    return s is int && s > 0 ? Duration(seconds: s) : null;
  }

  /// [m] left at [at] of [length]: remembered, unless near its start or its end.
  Future<void> leave(MediaEntry m, Duration at, Duration length) async {
    final points = await _all();
    final key = _key(m);
    points.remove(key);
    if (length > margin * 3 && at > margin && at < length - margin) points[key] = at.inSeconds;
    while (points.length > kept) {
      points.remove(points.keys.first);
    }
    final values = Map.of(points);
    _saving = _saving.then((_) => _store?.save(values)).catchError((Object _) {});
    await _saving;
  }
}
