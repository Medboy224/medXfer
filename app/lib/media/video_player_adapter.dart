/// The Android adapter of [MediaPlayer] (decision D8): Flutter's own `video_player` (BSD-3),
/// on ExoPlayer (Media3, Apache 2.0) under Android. It plays videos and songs alike, read from
/// their `content://` address.
library;

import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:video_player/video_player.dart';

import '../platform/library.dart';
import 'media_kit_adapter.dart';
import 'media_player.dart';
import 'two_engines.dart';

class VideoPlayerAdapter extends MediaPlayer {
  VideoPlayerController? _c;
  String? _failed;
  bool _disposed = false;

  VideoPlayerValue? get _v => _c?.value;

  @override
  Future<void> open(MediaEntry m) async {
    await _close();
    _failed = null;
    // An Android surface rather than a texture: the texture showed the decoder's padding, the
    // picture's last column and row smeared over 23 px at its right and bottom (1584 × 720
    // decoded in 1600 × 736, screenshot of 24 November 2026); the surface crops it. (A freeze
    // first blamed on it came from a damaged file: VLC stops at the same place.)
    // A file the system's library does not know (opened from the Files tab, its id below 0): by
    // its path.
    // Only for a picture that is there: a song, or a video without one, made the plugin's surface
    // throw « sendInitialized called with no video playing », and the app close (24 November
    // 2026). Android knows a video's size when it has a picture.
    final view = m.kind == MediaKind.video && m.width > 0 && m.height > 0 ? VideoViewType.platformView : VideoViewType.textureView;
    final c = m.id < 0
        ? VideoPlayerController.file(File(m.path), viewType: view)
        : VideoPlayerController.contentUri(contentUriOf(m), viewType: view);
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
  double get speed => _v?.playbackSpeed ?? 1;
  @override
  Future<void> setSpeed(double speed) async => _c?.setPlaybackSpeed(speed);
  @override
  bool get looping => _v?.isLooping ?? false;
  @override
  Future<void> setLooping(bool on) async => _c?.setLooping(on);
  @override
  String get engine => 'ExoPlayer';

  @override
  Widget view({BoxFit fit = BoxFit.contain}) {
    final c = _c;
    final size = _v?.size ?? Size.zero;
    if (c == null || size.isEmpty) return const SizedBox.shrink();
    // Laid out at the size it shows rather than scaled.
    return ClipRect(
      child: LayoutBuilder(
        builder: (context, box) {
          final out = shownSize(fit, size, box.biggest);
          return OverflowBox(
            minWidth: out.width,
            maxWidth: out.width,
            minHeight: out.height,
            maxHeight: out.height,
            child: VideoPlayer(c),
          );
        },
      ),
    );
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

/// Android: ExoPlayer first, libmpv for what it does not read ([TwoEngines]).
class AndroidMediaPlayers implements MediaPlayers {
  const AndroidMediaPlayers();

  @override
  bool get supported => true;

  @override
  MediaPlayer create() => TwoEngines(system: VideoPlayerAdapter.new, wide: MediaKitAdapter.new);
}

/// This system's players: Android now; Windows and Linux in WP-8.2b.
MediaPlayers systemMediaPlayers() => Platform.isAndroid ? const AndroidMediaPlayers() : const NoMediaPlayers();

/// The size a picture of [size] takes in [box]: whole ([BoxFit.contain]), covering it
/// ([BoxFit.cover], cropped by the caller), or stretched to it ([BoxFit.fill]).
Size shownSize(BoxFit fit, Size size, Size box) {
  if (fit == BoxFit.fill) return box;
  final sx = box.width / size.width, sy = box.height / size.height;
  final scale = fit == BoxFit.cover ? (sx > sy ? sx : sy) : (sx < sy ? sx : sy);
  return Size(size.width * scale, size.height * scale);
}
