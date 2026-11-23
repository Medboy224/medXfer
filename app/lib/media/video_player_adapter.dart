/// The Android adapter of [MediaPlayer] (decision D8): Flutter's own `video_player` (BSD-3),
/// on ExoPlayer (Media3, Apache 2.0) under Android. It plays videos and songs alike, read from
/// their `content://` address.
library;

import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:video_player/video_player.dart';

import '../platform/library.dart';
import 'media_player.dart';

class VideoPlayerAdapter extends MediaPlayer {
  VideoPlayerController? _c;
  String? _failed;
  bool _disposed = false;

  VideoPlayerValue? get _v => _c?.value;

  @override
  Future<void> open(MediaEntry m) async {
    await _close();
    _failed = null;
    final c = VideoPlayerController.contentUri(contentUriOf(m));
    _c = c;
    c.addListener(_changed);
    try {
      await c.initialize();
    } on Object catch (e) {
      _failed = '$e';
    }
    _changed();
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  Future<void> _close() async {
    final c = _c;
    _c = null;
    if (c == null) return;
    c.removeListener(_changed);
    await c.dispose();
  }

  @override
  bool get ready => _v?.isInitialized ?? false;
  @override
  bool get playing => _v?.isPlaying ?? false;
  @override
  bool get finished => _v?.isCompleted ?? false;
  @override
  Duration get position => _v?.position ?? Duration.zero;
  @override
  Duration get duration => _v?.duration ?? Duration.zero;
  @override
  double get volume => _v?.volume ?? 1;
  @override
  double get aspect {
    final v = _v;
    if (v == null || !v.isInitialized || v.size.isEmpty) return 0;
    return v.aspectRatio;
  }

  @override
  String? get error => _failed ?? (_v?.hasError ?? false ? _v!.errorDescription ?? 'error' : null);

  @override
  Future<void> play() async => _c?.play();
  @override
  Future<void> pause() async => _c?.pause();
  @override
  Future<void> seek(Duration to) async => _c?.seekTo(to);
  @override
  Future<void> setVolume(double volume) async => _c?.setVolume(volume.clamp(0, 1));

  @override
  Widget view() {
    final c = _c;
    return c == null ? const SizedBox.shrink() : VideoPlayer(c);
  }

  @override
  void dispose() {
    _disposed = true;
    final c = _c;
    _c = null;
    if (c != null) {
      c.removeListener(_changed);
      c.dispose();
    }
    super.dispose();
  }
}

class AndroidMediaPlayers implements MediaPlayers {
  const AndroidMediaPlayers();

  @override
  bool get supported => true;

  @override
  MediaPlayer create() => VideoPlayerAdapter();
}

/// This system's players: Android now; Windows and Linux in WP-8.2b.
MediaPlayers systemMediaPlayers() => Platform.isAndroid ? const AndroidMediaPlayers() : const NoMediaPlayers();
