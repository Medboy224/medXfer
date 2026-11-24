/// The second engine on Android (decided on 23 November 2026): libmpv, through media_kit (MIT).
/// Its libmpv and FFmpeg are built LGPL v3, without GPL parts (read in the library itself:
/// --disable-gpl --disable-nonfree, -Dgpl=false). It reads what the phone's own decoders do not:
/// the AVI in Xvid that ExoPlayer played with its sound and a black screen, WMV, FLV, Dolby,
/// DTS; and it gives the sound tracks and the subtitles a file carries (real test of 23
/// November 2026).
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import '../platform/library.dart';
import 'media_player.dart';

class MediaKitAdapter extends MediaPlayer {
  MediaKitAdapter() : _player = _newPlayer() {
    // Made with the player, before anything plays: without it libmpv plays the sound alone.
    _video = VideoController(_player);
    final s = _player.stream;
    _subscriptions = [
      s.playing.listen((_) => _changed()),
      s.completed.listen((_) => _changed()),
      s.position.listen((_) => _moved()),
      s.duration.listen((d) {
        if (d > Duration.zero) _loaded();
        _changed();
      }),
      s.videoParams.listen((_) => _changed()),
      s.tracks.listen((_) => _changed()),
      s.track.listen((_) => _changed()),
      s.subtitle.listen((_) => _changed()),
      s.rate.listen((_) => _changed()),
      s.volume.listen((_) => _changed()),
      s.playlistMode.listen((_) => _changed()),
      // Read as libmpv logs them, warnings among them: one while the file opens makes it
      // unreadable only if no length follows soon after (an AVI that played was said unreadable
      // at random, real test of 24 November 2026); after, the rest plays on.
      s.error.listen((e) {
        debugPrint('media_kit: $e');
        final loading = _loading;
        if (loading == null || loading.isCompleted) return;
        _pending ??= e;
        _grace ??= Timer(const Duration(milliseconds: 1500), () {
          _error ??= _pending;
          _loaded();
        });
      }),
      // At its end while it opens: it could not be read.
      s.completed.listen((done) {
        final loading = _loading;
        if (!done || loading == null || loading.isCompleted || _s.duration > Duration.zero) return;
        _error ??= _pending ?? 'unreadable';
        _loaded();
      }),
    ];
  }

  static Player _newPlayer() {
    MediaKit.ensureInitialized(); // once, at the first video it plays: nothing at the app's start
    return Player(configuration: const PlayerConfiguration(title: 'medXfer'));
  }

  final Player _player;
  late final VideoController _video;
  late final List<StreamSubscription<Object?>> _subscriptions;
  Completer<void>? _loading;
  String? _error, _pending;
  Timer? _grace;
  bool _ready = false, _disposed = false;
  DateTime _lastMove = DateTime(0);

  PlayerState get _s => _player.state;

  void _loaded() {
    _grace?.cancel();
    _grace = null;
    final loading = _loading;
    if (loading != null && !loading.isCompleted) loading.complete();
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  /// The position, told 5 times a second: libmpv gives it at each frame, and the whole screen
  /// would be built again as often.
  void _moved() {
    final now = DateTime.now();
    if (now.difference(_lastMove) < const Duration(milliseconds: 200)) return;
    _lastMove = now;
    _changed();
  }

  @override
  Future<void> open(MediaEntry m) async {
    _error = null;
    _pending = null;
    _ready = false;
    final loading = _loading = Completer<void>();
    _changed();
    // Read where it is: the media permission lets it read the file itself.
    await _player.open(Media(m.path), play: false);
    // Open once its length is known; a file that never answers is said unreadable.
    await loading.future.timeout(const Duration(seconds: 12), onTimeout: () => _error ??= 'timeout');
    if (_disposed) return;
    _ready = _error == null;
    _changed();
  }

  @override
  bool get ready => _ready;
  @override
  bool get playing => _s.playing;
  @override
  bool get finished => _s.completed;
  @override
  Duration get position => _s.position;
  @override
  Duration get duration => _s.duration;
  @override
  double get volume => _s.volume / 100;
  @override
  String? get error => _error;
  @override
  String get engine => 'libmpv';

  @override
  double get aspect {
    final p = _s.videoParams;
    final w = p.dw ?? _s.width, h = p.dh ?? _s.height;
    if (w == null || h == null || w <= 0 || h <= 0) return 0;
    return (p.rotate ?? 0) % 180 != 0 ? h / w : w / h;
  }

  @override
  Future<void> play() => _player.play();
  @override
  Future<void> pause() => _player.pause();
  @override
  Future<void> seek(Duration to) => _player.seek(to);
  @override
  Future<void> setVolume(double volume) => _player.setVolume(volume.clamp(0, 1) * 100);

  @override
  double get speed => _s.rate;
  @override
  Future<void> setSpeed(double speed) => _player.setRate(speed);
  @override
  bool get looping => _s.playlistMode == PlaylistMode.single;
  @override
  Future<void> setLooping(bool on) => _player.setPlaylistMode(on ? PlaylistMode.single : PlaylistMode.none);

  static bool _real(String id) => id != 'auto' && id != 'no';

  /// Its name and language as the file gives them; empty when it gives none (the screen then
  /// numbers it).
  static String _label(String? title, String? language) =>
      [title, language].whereType<String>().map((s) => s.trim()).where((s) => s.isNotEmpty).join(' · ');

  @override
  List<MediaTrack> get audioTracks => [
    for (final t in _s.tracks.audio)
      if (_real(t.id)) MediaTrack(t.id, _label(t.title, t.language)),
  ];

  @override
  List<MediaTrack> get subtitleTracks => [
    for (final t in _s.tracks.subtitle)
      if (_real(t.id)) MediaTrack(t.id, _label(t.title, t.language)),
  ];

  /// 'auto': the one libmpv chose by itself, the file's default.
  @override
  String? get audioTrack {
    final id = _s.track.audio.id;
    if (_real(id)) return id;
    final all = audioTracks;
    return all.isEmpty ? null : all.first.id;
  }

  @override
  String? get subtitleTrack {
    final id = _s.track.subtitle.id;
    return _real(id) ? id : null;
  }

  @override
  Future<void> setAudioTrack(String id) async {
    for (final t in _s.tracks.audio) {
      if (t.id == id) return _player.setAudioTrack(t);
    }
  }

  @override
  Future<void> setSubtitleTrack(String? id) async {
    if (id == null) return _player.setSubtitleTrack(SubtitleTrack.no());
    for (final t in _s.tracks.subtitle) {
      if (t.id == id) return _player.setSubtitleTrack(t);
    }
  }

  @override
  String get subtitle => _s.subtitle.map((s) => s.trim()).where((s) => s.isNotEmpty).join('\n');

  @override
  Widget view({BoxFit fit = BoxFit.contain}) => Video(
    controller: _video,
    fit: fit,
    controls: NoVideoControls,
    // The screen keeps the phone awake while it plays (AppState.playing), and draws the
    // subtitles itself, above its controls.
    wakelock: false,
    subtitleViewConfiguration: const SubtitleViewConfiguration(visible: false),
  );

  @override
  void dispose() {
    _disposed = true;
    _grace?.cancel();
    for (final s in _subscriptions) {
      unawaited(s.cancel());
    }
    unawaited(_player.dispose());
    super.dispose();
  }
}
