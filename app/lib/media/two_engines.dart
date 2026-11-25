/// Two engines behind one player (decided on 23 November 2026): the system's, ExoPlayer on
/// Android (the phone's own hardware decoders, light on the battery), and libmpv for what it does
/// not read. A file ExoPlayer is known not to read goes straight to libmpv; any other starts with
/// ExoPlayer, and goes to libmpv if ExoPlayer fails, or plays a video without its picture (the
/// AVI in Xvid of the real test of 23 November 2026: its sound, and a black screen).
library;

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../platform/library.dart';
import 'media_player.dart';

class TwoEngines extends MediaPlayer {
  TwoEngines({required this.system, required this.wide});

  /// Each engine, made once it is needed.
  final MediaPlayer Function() system, wide;

  MediaPlayer? _system, _wide, _current;
  bool _disposed = false;
  double _speed = 1;
  bool _looping = false;

  /// What ExoPlayer does not read, or not whole, by the file's extension. MKV goes to libmpv for
  /// its subtitles and its sound tracks, which ExoPlayer here does not offer.
  static const wideOnly = {
    'avi', 'divx', 'xvid', 'wmv', 'asf', 'flv', 'f4v', 'mkv', 'rm', 'rmvb', 'vob', 'ogv', 'ogm', //
    'wma', 'ape', 'mka', 'tta', 'wv', 'ra', 'ac3', 'eac3', 'dts',
  };

  static bool wideOnlyFor(MediaEntry m) {
    final dot = m.name.lastIndexOf('.');
    return dot >= 0 && wideOnly.contains(m.name.substring(dot + 1).toLowerCase());
  }

  MediaEntry? _entry;
  bool _opening = false, _rescued = false;

  @override
  Future<void> open(MediaEntry m) async {
    _entry = m;
    _rescued = false;
    _opening = true;
    try {
      if (!wideOnlyFor(m)) {
        final p = _use(wideEngine: false);
        await p.open(m);
        if (_disposed) return;
        final noPicture = m.kind == MediaKind.video && p.error == null && p.ready && p.aspect == 0;
        if (p.error == null && !noPicture) {
          await _settle(p);
          return;
        }
      }
      final w = _use(wideEngine: true);
      await w.open(m);
      if (!_disposed && w.error == null) await _settle(w);
    } finally {
      _opening = false;
    }
  }

  /// ExoPlayer failing once open, as it starts to play (a decoder refused): libmpv goes on
  /// from there, rather than « ce téléphone ne lit pas ce format ».
  Future<void> _rescue(MediaEntry m, Duration at) async {
    final w = _use(wideEngine: true);
    await w.open(m);
    if (_disposed || !identical(_entry, m) || w.error != null) return;
    await _settle(w);
    if (at > Duration.zero) await w.seek(at);
    await w.play();
  }

  /// The speed and the repeat chosen, kept from one file to the next, whichever engine plays it.
  Future<void> _settle(MediaPlayer p) async {
    if (_speed != 1) await p.setSpeed(_speed);
    if (_looping) await p.setLooping(true);
  }

  MediaPlayer _use({required bool wideEngine}) {
    // The other one let go: two engines each holding a decoder, the second might get none.
    final other = wideEngine ? _system : _wide;
    if (other != null) {
      other.removeListener(_changed);
      other.dispose();
      if (wideEngine) {
        _system = null;
      } else {
        _wide = null;
      }
    }
    var p = wideEngine ? _wide : _system;
    if (p == null) {
      p = (wideEngine ? wide : system)()..addListener(_changed);
      if (wideEngine) {
        _wide = p;
      } else {
        _system = p;
      }
    }
    _current = p;
    return p;
  }

  void _changed() {
    if (_disposed) return;
    final p = _current, m = _entry;
    if (!_opening && !_rescued && p != null && identical(p, _system) && p.error != null && m != null) {
      _rescued = true;
      final at = p.position;
      // Not from within its own notice: the engine that tells it is let go.
      unawaited(Future(() => _rescue(m, at)));
    }
    notifyListeners();
  }

  @override
  bool get ready => _current?.ready ?? false;
  @override
  bool get playing => _current?.playing ?? false;
  @override
  bool get finished => _current?.finished ?? false;
  @override
  Duration get position => _current?.position ?? Duration.zero;
  @override
  Duration get duration => _current?.duration ?? Duration.zero;
  @override
  double get volume => _current?.volume ?? 1;
  @override
  double get aspect => _current?.aspect ?? 0;
  @override
  String? get error => _current?.error;
  @override
  String? get engine => _current?.engine;

  @override
  Future<void> play() async => _current?.play();
  @override
  Future<void> pause() async => _current?.pause();
  @override
  Future<void> seek(Duration to) async => _current?.seek(to);
  @override
  Future<void> setVolume(double volume) async => _current?.setVolume(volume);

  @override
  double get speed => _current?.speed ?? _speed;
  @override
  Future<void> setSpeed(double speed) async {
    _speed = speed;
    await _current?.setSpeed(speed);
  }

  @override
  bool get looping => _current?.looping ?? _looping;
  @override
  Future<void> setLooping(bool on) async {
    _looping = on;
    await _current?.setLooping(on);
  }

  @override
  List<MediaTrack> get audioTracks => _current?.audioTracks ?? const [];
  @override
  List<MediaTrack> get subtitleTracks => _current?.subtitleTracks ?? const [];
  @override
  String? get audioTrack => _current?.audioTrack;
  @override
  String? get subtitleTrack => _current?.subtitleTrack;
  @override
  Future<void> setAudioTrack(String id) async => _current?.setAudioTrack(id);
  @override
  Future<void> setSubtitleTrack(String? id) async => _current?.setSubtitleTrack(id);
  @override
  String get subtitle => _current?.subtitle ?? '';

  @override
  Widget view({BoxFit fit = BoxFit.contain}) => _current?.view(fit: fit) ?? const SizedBox.shrink();

  @override
  void dispose() {
    _disposed = true;
    for (final p in [_system, _wide]) {
      p?.removeListener(_changed);
      p?.dispose();
    }
    super.dispose();
  }
}
