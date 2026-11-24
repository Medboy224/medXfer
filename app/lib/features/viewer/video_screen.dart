/// A video (WP-8.2, mockups 24 to 26 of the « Version 4 » canvas, agreed on 23 November 2026):
/// the picture over the whole screen and its commands laid on it, the same in both orientations,
/// hidden after 3 s of playing and back at a touch. The gestures of a phone's video player: two
/// touches at a side, ±10 s, and each one more in the run 10 s again; sliding along, the
/// position; up and down, the brightness at the left and the volume at the right; held, ×2;
/// pinched, the picture fills the screen, or comes back whole. Nothing starts at the sides, left
/// to Android's back gesture. The menu ⋮: speed, sound track, subtitles, picture, repeat,
/// details, another app, share; « À suivre » and its autoplay. Each video goes on where it was
/// left, with « Recommencer » at hand.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter/services.dart';
import 'package:intl/intl.dart' show NumberFormat;

import '../../app.dart';
import '../../app_state.dart';
import '../../l10n/app_localizations.dart';
import '../../library/explorer.dart';
import '../../media/media_player.dart';
import '../../media/video_player_adapter.dart' show shownSize;
import '../../platform/library.dart';
import '../../theme.dart';
import '../../widgets/format.dart';
import '../../widgets/v4.dart';
import '../explorer/explorer_tabs.dart' show LibraryImage;
import 'open_media.dart';
import 'player_controls.dart';

/// A position or a length as a player's clock shows it: 0:30, 18:20, 1:56:12.
String clock(Duration d) {
  final s = d.inSeconds;
  String two(int v) => v.toString().padLeft(2, '0');
  final h = s ~/ 3600, m = (s % 3600) ~/ 60, sec = s % 60;
  return h > 0 ? '$h:${two(m)}:${two(sec)}' : '$m:${two(sec)}';
}

class VideoScreen extends StatefulWidget {
  const VideoScreen({super.key, required this.items, required this.index});

  final List<MediaEntry> items;
  final int index;

  @override
  State<VideoScreen> createState() => _VideoScreenState();
}

enum _Panel { none, menu, speed, audio, subtitles, picture, info, next }

/// How the picture takes the screen (« Format de l'image », what is needed of a player's list):
/// whole, filling it cropped, stretched to it, or at 16:9 or 4:3.
enum _Picture { fit, crop, stretch, wide, tv }

/// What a finger moving on the picture does, decided once it has gone far enough.
enum _Drag { none, undecided, seek, brightness, volume, pinch, ignored }

class _VideoScreenState extends State<VideoScreen> {
  late final AppState _state;
  late final MediaPlayer _player;
  late int _index = widget.index;
  bool _started = false;

  bool _chrome = true; // the commands shown
  Timer? _hide;
  bool _wasPlaying = false, _wasFinished = false;
  _Panel _panel = _Panel.none;
  _Picture _picture = _Picture.fit;
  bool get _fill => _picture != _Picture.fit;
  Timer? _keep; // where it is, remembered every few seconds while it plays
  AppLifecycleListener? _life;
  Timer? _scrubbing; // a seek at most every 150 ms while a finger slides
  // A finger on the bar: the commands stay. Hidden under it, the bar never heard the finger lift,
  // and the large time stayed on the screen (real test of 24 November 2026).
  bool _sliding = false;
  bool _leaving = false; // the page closing: the picture gone already
  Duration? _scrubTo;
  String? _lastSubtitle; // the subtitles turned off by CC, turned back on by it

  Duration? _resumed; // « Reprise à … · Recommencer », for a few seconds
  Timer? _resumeTimer;

  // Taps: a second one soon after, on the same side, seeks; each one more in the run again.
  Offset? _tapAt;
  Timer? _single;
  int _skipSide = 0; // -1 back, 1 forward, 0 none
  int _skipSeconds = 0;
  Duration _skipFrom = Duration.zero;
  Timer? _skipRun;

  // A finger moving, or two pinching.
  _Drag _drag = _Drag.none;
  Offset _dragFrom = Offset.zero;
  Duration _seekFrom = Duration.zero;
  Duration? _seekTo; // shown large while the finger slides, and on the bar
  _Drag _levelKind = _Drag.none;
  double? _level; // the brightness or the volume being set, shown in its bar
  double _levelFrom = 0;
  double? _levelSet; // the last one given to the system
  Timer? _levelTimer;
  bool _pinched = false;
  double? _brightness, _volume; // as read when the player opened, then as set
  bool _brightnessSet = false;

  double? _speedBefore; // held: ×2 until the finger lifts
  String? _hint; // « Remplir l'écran », « Image entière »
  Timer? _hintTimer;

  static const _tapGap = Duration(milliseconds: 280);
  static const _hideAfter = Duration(seconds: 3);
  static const _onAccent = Color(0xFF0D0F0E);
  static const _panelColor = Color(0xFF151816);
  static const speeds = [0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0];

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_started) return;
    _started = true;
    _state = AppScope.of(context, listen: false).state;
    _player = _state.players.create()..addListener(_changed);
    // The whole screen, as a video player: the system's bars come back with a swipe.
    unawaited(SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky));
    unawaited(_state.screen.setUnderNotch(_state.settings.videoUnderNotch));
    unawaited(_readLevels());
    // Where it is, kept as the app goes to the background too: killed there, or closed from
    // the recent apps, it was lost (« pas à chaque fois », real test of 24 November 2026).
    _life = AppLifecycleListener(onHide: _leave);
    _keep = Timer.periodic(const Duration(seconds: 5), (_) {
      if (_player.playing) _leave();
    });
    unawaited(_open(_index));
  }

  Future<void> _readLevels() async {
    final b = await _state.screen.brightness();
    final v = await _state.screen.volume();
    _brightness ??= b;
    _volume ??= v;
  }

  /// What the player says beyond what the screen shows: the phone kept awake while it plays,
  /// the commands hidden once it plays, the next video at the end of one.
  void _changed() {
    final p = _player;
    _state.playing(p.playing);
    if (p.playing != _wasPlaying) {
      _wasPlaying = p.playing;
      if (p.playing) {
        _armHide();
      } else {
        _hide?.cancel();
      }
    }
    if (p.finished && !_wasFinished) {
      _wasFinished = true;
      if (_state.settings.videoAutoplay && !p.looping && _index < widget.items.length - 1) {
        unawaited(_open(_index + 1));
      } else if (mounted && !_chrome) {
        setState(() => _chrome = true);
      }
    }
  }

  Future<void> _open(int i) async {
    if (i < 0 || i >= widget.items.length) return;
    _leave();
    setState(() {
      _index = i;
      _resumed = null;
      _wasFinished = false;
      _chrome = true;
      _panel = _Panel.none;
      _seekTo = null;
    });
    final m = widget.items[i];
    await _player.open(m);
    if (!mounted || _index != i || _player.error != null) return;
    final at = await _state.resume.of(m);
    if (!mounted || _index != i) return;
    if (at != null && at < _player.duration) {
      await _player.seek(at);
      if (!mounted) return;
      setState(() => _resumed = at);
      _resumeTimer?.cancel();
      _resumeTimer = Timer(const Duration(seconds: 5), () {
        if (mounted) setState(() => _resumed = null);
      });
    }
    await _player.play();
  }

  /// The video left: where it was, remembered.
  void _leave() {
    final p = _player;
    if (!p.ready || p.error != null) return;
    unawaited(_state.resume.leave(widget.items[_index], p.position, p.duration));
  }

  @override
  void dispose() {
    _leave();
    for (final t in [_hide, _resumeTimer, _single, _skipRun, _levelTimer, _hintTimer, _keep, _scrubbing]) {
      t?.cancel();
    }
    _life?.dispose();
    _state.playing(false);
    if (_brightnessSet) unawaited(_state.screen.setBrightness(null));
    unawaited(_state.screen.setUnderNotch(null));
    unawaited(SystemChrome.setPreferredOrientations(const []));
    unawaited(SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge));
    _player
      ..removeListener(_changed)
      ..dispose();
    super.dispose();
  }

  // ——— The commands shown, then hidden ———

  void _armHide() {
    _hide?.cancel();
    if (!_player.playing || _panel != _Panel.none) return;
    _hide = Timer(_hideAfter, () {
      if (mounted && _player.playing && _panel == _Panel.none && _drag == _Drag.none && !_sliding) setState(() => _chrome = false);
    });
  }

  void _toggleChrome() {
    setState(() => _chrome = !_chrome);
    if (_chrome) _armHide();
  }

  /// A command used: the commands stay 3 s more.
  void _touched() {
    if (!_chrome) setState(() => _chrome = true);
    _armHide();
  }

  void _openPanel(_Panel panel) {
    _hide?.cancel();
    setState(() {
      _panel = panel;
      _chrome = true;
    });
  }

  void _closePanel() {
    setState(() => _panel = _Panel.none);
    _armHide();
  }

  // ——— The gestures ———

  /// Where Android's back gesture starts: no gesture of the player begins there.
  bool _atSide(Offset global) {
    final sides = MediaQuery.systemGestureInsetsOf(context);
    final width = MediaQuery.sizeOf(context).width;
    return global.dx < sides.left || global.dx > width - sides.right;
  }

  /// The third of the picture a touch falls in: -1 the left, 0 the middle, 1 the right.
  static int _third(Offset at, Size size) => at.dx < size.width / 3 ? -1 : (at.dx > size.width * 2 / 3 ? 1 : 0);

  void _tapUp(TapUpDetails d, Size size) {
    final side = _third(d.localPosition, size);
    if ((_skipRun?.isActive ?? false) && side == _skipSide && side != 0) {
      _skip(side);
      return;
    }
    final at = _tapAt;
    if ((_single?.isActive ?? false) && at != null && (d.localPosition - at).distance < 96 && _third(at, size) == side) {
      _single?.cancel();
      _tapAt = null;
      // Two touches in the middle: pause, or play again (asked on 24 November 2026).
      if (side == 0) {
        if (_player.ready) unawaited(_playOrPause());
      } else {
        _skip(side);
      }
      return;
    }
    _tapAt = d.localPosition;
    _single?.cancel();
    _single = Timer(_tapGap, () {
      _tapAt = null;
      if (mounted) _toggleChrome();
    });
  }

  /// ±10 s, and 10 s more at each touch of the run, from where the run began.
  void _skip(int side) {
    final p = _player;
    if (!p.ready) return;
    if (side != _skipSide || !(_skipRun?.isActive ?? false)) {
      _skipSide = side;
      _skipSeconds = 0;
      _skipFrom = p.position;
    }
    _skipSeconds += 10;
    var to = _skipFrom + Duration(seconds: _skipSeconds * side);
    if (to < Duration.zero) to = Duration.zero;
    if (p.duration > Duration.zero && to > p.duration) to = p.duration;
    unawaited(p.seek(to));
    _skipRun?.cancel();
    _skipRun = Timer(const Duration(milliseconds: 800), () {
      if (mounted) setState(() => _skipSide = 0);
    });
    setState(() {});
  }

  void _scaleStart(ScaleStartDetails d) {
    if (d.pointerCount < 2 && _atSide(d.focalPoint)) {
      _drag = _Drag.ignored;
      return;
    }
    _drag = d.pointerCount >= 2 ? _Drag.pinch : _Drag.undecided;
    _dragFrom = d.localFocalPoint;
    _pinched = false;
    _levelSet = null;
  }

  void _scaleUpdate(ScaleUpdateDetails d, Size size) {
    if (_drag == _Drag.ignored || _drag == _Drag.none) return;
    if (d.pointerCount >= 2 || _drag == _Drag.pinch) {
      if (_drag != _Drag.pinch) {
        _drag = _Drag.pinch;
        setState(() {
          _seekTo = null;
          _level = null;
        });
      }
      if (!_pinched && d.pointerCount >= 2 && (d.scale > 1.12 || d.scale < 0.9)) {
        _pinched = true;
        _setPicture(d.scale > 1 ? _Picture.crop : _Picture.fit);
      }
      return;
    }
    final moved = d.localFocalPoint - _dragFrom;
    if (_drag == _Drag.undecided) {
      if (moved.distance < 16) return;
      final p = _player;
      if (moved.dx.abs() > moved.dy.abs()) {
        if (!p.ready || p.duration <= Duration.zero) {
          _drag = _Drag.ignored;
          return;
        }
        _drag = _Drag.seek;
        _seekFrom = p.position;
        _hide?.cancel();
      } else {
        final left = _dragFrom.dx < size.width / 2;
        final screen = _state.screen.supported;
        if (left && !screen) {
          _drag = _Drag.ignored; // no brightness to set here
          return;
        }
        _drag = left ? _Drag.brightness : _Drag.volume;
        _levelFrom = (left ? _brightness : (screen ? _volume : p.volume)) ?? 0.5;
      }
    }
    switch (_drag) {
      case _Drag.seek:
        final total = _player.duration;
        // The whole width: two minutes, or the whole video when it is shorter.
        final span = total < const Duration(minutes: 2) ? total : const Duration(minutes: 2);
        var to = _seekFrom + span * (moved.dx / size.width);
        if (to < Duration.zero) to = Duration.zero;
        if (to > total) to = total;
        _scrub(to);
      case _Drag.brightness || _Drag.volume:
        _setLevel(_drag, (_levelFrom - moved.dy / (size.height * 0.75)).clamp(0.0, 1.0));
      default:
        break;
    }
  }

  /// The finger sliding (on the picture, or the bar): the picture follows it, seeking at most
  /// every 150 ms; it only moved once the finger lifted (real test of 24 November 2026).
  void _scrub(Duration to) {
    setState(() => _seekTo = to);
    _scrubTo = to;
    if (_scrubbing?.isActive ?? false) return;
    unawaited(_player.seek(to));
    _scrubbing = Timer(const Duration(milliseconds: 150), () {
      final last = _scrubTo;
      if (mounted && last != null && last != to) unawaited(_player.seek(last));
    });
  }

  void _setLevel(_Drag kind, double v) {
    _levelTimer?.cancel();
    setState(() {
      _level = v;
      _levelKind = kind;
    });
    final set = _levelSet;
    if (set != null && (set - v).abs() < 0.01) return;
    _levelSet = v;
    if (kind == _Drag.brightness) {
      _brightness = v;
      _brightnessSet = true;
      unawaited(_state.screen.setBrightness(v));
    } else if (_state.screen.supported) {
      _volume = v;
      unawaited(_state.screen.setVolume(v));
    } else {
      unawaited(_player.setVolume(v));
    }
  }

  void _scaleEnd(ScaleEndDetails d) {
    final was = _drag;
    _drag = _Drag.none;
    final to = _seekTo;
    if (was == _Drag.seek && to != null) unawaited(_player.seek(to));
    if (was == _Drag.seek || was == _Drag.brightness || was == _Drag.volume) {
      _clearSoon();
      _armHide();
    }
  }

  /// The large position and the level bar, gone a moment after the finger lifts.
  void _clearSoon() {
    _levelTimer?.cancel();
    _levelTimer = Timer(const Duration(milliseconds: 700), () {
      if (mounted) {
        setState(() {
          _level = null;
          _seekTo = null;
        });
      }
    });
  }

  void _holdStart(LongPressStartDetails d) {
    final p = _player;
    if (_atSide(d.globalPosition) || !p.ready || !p.playing) return;
    _speedBefore = p.speed;
    unawaited(p.setSpeed(2));
    setState(() {});
  }

  void _holdEnd() {
    final before = _speedBefore;
    if (before == null) return;
    _speedBefore = null;
    unawaited(_player.setSpeed(before));
    setState(() {});
  }

  String _pictureLabel(AppLocalizations l, _Picture p) => switch (p) {
    _Picture.fit => l.pictureFit,
    _Picture.crop => l.pictureCrop,
    _Picture.stretch => l.pictureStretch,
    _Picture.wide => '16:9',
    _Picture.tv => '4:3',
  };

  void _setPicture(_Picture picture) {
    final l = AppLocalizations.of(context);
    setState(() {
      _picture = picture;
      _hint = picture == _Picture.fit ? l.wholePicture : (picture == _Picture.crop ? l.fillScreen : _pictureLabel(l, picture));
    });
    _hintTimer?.cancel();
    _hintTimer = Timer(const Duration(milliseconds: 1200), () {
      if (mounted) setState(() => _hint = null);
    });
  }

  void _rotate() {
    _touched();
    final portrait = MediaQuery.orientationOf(context) == Orientation.portrait;
    unawaited(
      SystemChrome.setPreferredOrientations(
        portrait ? const [DeviceOrientation.landscapeLeft, DeviceOrientation.landscapeRight] : const [DeviceOrientation.portraitUp],
      ),
    );
  }

  void _toggleSubtitles() {
    _touched();
    final p = _player;
    final now = p.subtitleTrack;
    if (now != null) {
      _lastSubtitle = now;
      unawaited(p.setSubtitleTrack(null));
      return;
    }
    final tracks = p.subtitleTracks;
    if (tracks.isNotEmpty) unawaited(p.setSubtitleTrack(_lastSubtitle ?? tracks.first.id));
  }

  Future<void> _playOrPause() async {
    _touched();
    final p = _player;
    if (p.playing) return p.pause();
    if (p.finished) await p.seek(Duration.zero);
    await p.play();
  }

  String _speedLabel(double s) => '${NumberFormat.decimalPattern(Localizations.localeOf(context).toString()).format(s)}×';

  String _trackLabel(AppLocalizations l, List<MediaTrack> tracks, String? id) {
    for (final (i, t) in tracks.indexed) {
      if (t.id == id) return t.label.isEmpty ? l.trackNumber(i + 1) : t.label;
    }
    return l.subtitlesOff;
  }

  // ——— The screen ———

  @override
  Widget build(BuildContext context) {
    final accent = playerAccent(context);
    return Theme(
      data: ThemeData(
        brightness: Brightness.dark,
        useMaterial3: true,
        fontFamily: 'Archivo',
        colorScheme: ColorScheme.dark(primary: accent, secondary: accent, onPrimary: _onAccent),
      ),
      child: PopScope(
        canPop: _panel == _Panel.none,
        // Back closes an open panel first.
        onPopInvokedWithResult: (didPop, _) {
          if (!didPop) {
            _closePanel();
            return;
          }
          // Leaving: the picture goes at once, and the sound with it. Drawn by Android apart from
          // the rest, it followed none of the transition: it stayed whole over the grid, still
          // playing, until the page was gone (filmed on 24 November 2026).
          setState(() => _leaving = true);
          unawaited(_player.pause());
        },
        child: Scaffold(
          backgroundColor: Colors.black,
          body: ListenableBuilder(
            listenable: _player,
            builder: (context, _) => LayoutBuilder(builder: (context, box) => _screen(context, box.biggest)),
          ),
        ),
      ),
    );
  }

  Widget _screen(BuildContext context, Size size) {
    final l = AppLocalizations.of(context);
    final accent = playerAccent(context);
    final p = _player;
    final m = widget.items[_index];
    final failed = p.error != null;
    final wide = size.width > size.height;
    final chrome = _chrome || failed || !p.ready;
    final subtitle = p.subtitle;
    final resumed = _resumed;
    // Each layer keyed: a layer shown on the way (the large time) came before the commands, and
    // Flutter, matching them by their order, made the bar anew under the finger: it never heard
    // the finger lift, the large time stayed on the screen (real test of 24 November 2026).
    return Stack(
      fit: StackFit.expand,
      children: [
        if (p.ready && !failed && !_leaving) Positioned.fill(key: const ValueKey('layer-picture'), child: _shown(p)),
        if (!p.ready && !failed) const Center(key: ValueKey('layer-wait'), child: CircularProgressIndicator()),
        Positioned.fill(key: const ValueKey('layer-surface'), child: _surface(l, size)),
        ..._feedback(l, size, accent),
        if (chrome) _topBar(context, l, m, wide),
        // Under the picture's middle: the subtitles, « Reprise à … », the commands; one above the
        // other, never over each other.
        Positioned(
          key: const ValueKey('layer-bottom'),
          left: 0,
          right: 0,
          bottom: 0,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (subtitle.isNotEmpty && !failed) _Caption(text: subtitle),
              if (resumed != null && !failed) _resumeToast(context, l, accent, resumed),
              if (chrome && !failed) KeyedSubtree(key: const ValueKey('commands'), child: _commands(context, l, accent, m, wide)) else const SizedBox(height: 24),
            ],
          ),
        ),
        if (failed)
          Center(
            key: const ValueKey('layer-failed'),
            child: SingleChildScrollView(
              child: CannotPlay(
                accent: accent,
                unsupported: !_state.players.supported,
                detail: p.error,
                onOpenWith: _state.openWith.supported ? () => unawaited(_state.openWith.open(m)) : null,
              ),
            ),
          ),
        if (_panel != _Panel.none) ...[
          Positioned.fill(
            child: GestureDetector(
              key: const Key('video-panel-scrim'),
              onTap: _closePanel,
              child: const ColoredBox(color: Color(0x73000000)),
            ),
          ),
          _panelBox(context, l, accent, m, size, wide),
        ],
      ],
    );
  }

  /// The picture as « Format de l'image » sets it; 16:9 and 4:3 stretch it to that shape, the
  /// largest that fits.
  /// Always the same widgets around the picture, only their sizes changing: another shape made
  /// the picture's view anew, an Android surface among them.
  Widget _shown(MediaPlayer p) {
    final ratio = switch (_picture) {
      _Picture.wide => 16 / 9,
      _Picture.tv => 4 / 3,
      _ => null,
    };
    final fit = switch (_picture) {
      _Picture.fit => BoxFit.contain,
      _Picture.crop => BoxFit.cover,
      _ => BoxFit.fill,
    };
    return LayoutBuilder(
      builder: (context, box) {
        final size = ratio == null ? box.biggest : shownSize(BoxFit.contain, Size(ratio, 1), box.biggest);
        return Center(
          child: SizedBox(
            key: const ValueKey('picture-box'),
            width: size.width,
            height: size.height,
            child: AspectRatio(aspectRatio: size.width / size.height, child: p.view(fit: fit)),
          ),
        );
      },
    );
  }

  Widget _surface(AppLocalizations l, Size size) => Semantics(
    label: l.videoSurface,
    button: true,
    onTap: _toggleChrome,
    customSemanticsActions: {
      CustomSemanticsAction(label: l.seekBack10): () => _skip(-1),
      CustomSemanticsAction(label: l.seekForward10): () => _skip(1),
    },
    child: GestureDetector(
      key: const Key('video-surface'),
      behavior: HitTestBehavior.opaque,
      excludeFromSemantics: true,
      onTapUp: (d) => _tapUp(d, size),
      onLongPressStart: _holdStart,
      onLongPressEnd: (_) => _holdEnd(),
      onScaleStart: _scaleStart,
      onScaleUpdate: (d) => _scaleUpdate(d, size),
      onScaleEnd: _scaleEnd,
    ),
  );

  /// What a gesture does, shown as it does it.
  List<Widget> _feedback(AppLocalizations l, Size size, Color accent) {
    final p = _player;
    final top = MediaQuery.paddingOf(context).top + 20;
    final out = <Widget>[];
    if (_skipSide != 0) {
      final back = _skipSide < 0;
      out
        ..add(
          Positioned(
            left: back ? -size.width * 0.25 : null,
            right: back ? null : -size.width * 0.25,
            top: -size.height * 0.2,
            width: size.width * 0.62,
            height: size.height * 1.4,
            child: const IgnorePointer(child: DecoratedBox(decoration: ShapeDecoration(shape: OvalBorder(), color: Color(0x24FFFFFF)))),
          ),
        )
        ..add(
          Positioned(
            left: back ? size.width * 0.06 : null,
            right: back ? null : size.width * 0.06,
            top: size.height / 2 - 40,
            width: size.width * 0.3,
            child: IgnorePointer(
              child: Column(
                key: const Key('video-skip'),
                children: [
                  Icon(back ? Icons.fast_rewind_rounded : Icons.fast_forward_rounded, size: 36),
                  const SizedBox(height: 4),
                  Text('${back ? '−' : '+'}$_skipSeconds s', style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w800)),
                ],
              ),
            ),
          ),
        );
    }
    final level = _level;
    if (level != null && (_levelKind == _Drag.brightness || _levelKind == _Drag.volume)) {
      final light = _levelKind == _Drag.brightness;
      out.add(
        Positioned(
          left: light ? 48 : null,
          right: light ? null : 48,
          // At most 240 high, in the middle: upright, 64 % of the screen was far too long (real
          // test of 24 November 2026).
          top: (size.height - (size.height * 0.62 > 240 ? 240 : size.height * 0.62)) / 2,
          height: size.height * 0.62 > 240 ? 240 : size.height * 0.62,
          width: 48,
          child: IgnorePointer(
            child: Semantics(
              liveRegion: true,
              label: '${light ? l.brightnessLabel : l.volumeLabel} ${(level * 100).round()} %',
              child: Container(
                key: const Key('video-level'),
                padding: const EdgeInsets.symmetric(vertical: 14),
                decoration: BoxDecoration(color: const Color(0xE0151816), borderRadius: BorderRadius.circular(24)),
                child: Column(
                  children: [
                    Text('${(level * 100).round()}', style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w800)),
                    const SizedBox(height: 10),
                    Expanded(
                      child: Container(
                        width: 6,
                        decoration: BoxDecoration(color: Colors.white24, borderRadius: BorderRadius.circular(3)),
                        alignment: Alignment.bottomCenter,
                        child: FractionallySizedBox(
                          heightFactor: level,
                          child: Container(decoration: BoxDecoration(color: accent, borderRadius: BorderRadius.circular(3))),
                        ),
                      ),
                    ),
                    const SizedBox(height: 10),
                    Icon(light ? Icons.light_mode_rounded : Icons.volume_up_rounded, size: 20),
                  ],
                ),
              ),
            ),
          ),
        ),
      );
    }
    final seekTo = _seekTo;
    if (seekTo != null && !_sliding && _drag != _Drag.seek && !(_levelTimer?.isActive ?? false)) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && !_sliding && _drag != _Drag.seek) _clearSoon();
      });
    }
    if (seekTo != null) {
      final delta = seekTo - p.position;
      final sign = delta.isNegative ? '−' : '+';
      out.add(
        Positioned(
          left: 0,
          right: 0,
          top: size.height / 2 - 52,
          child: IgnorePointer(
            child: Column(
              key: const Key('video-seek-label'),
              children: [
                Text(clock(seekTo), style: const TextStyle(fontSize: 34, fontWeight: FontWeight.w800, fontFeatures: tabular)),
                Text('$sign${clock(delta.abs())}', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w800, color: accent, fontFeatures: tabular)),
              ],
            ),
          ),
        ),
      );
    }
    final hint = _hint;
    if (_speedBefore != null || hint != null) {
      out.add(
        Positioned(
          left: 0,
          right: 0,
          top: top,
          child: IgnorePointer(
            child: Center(
              child: _Pill(
                key: Key(_speedBefore != null ? 'video-fast' : 'video-hint'),
                children: _speedBefore != null ? const [Text('×2'), SizedBox(width: 6), Icon(Icons.fast_forward_rounded, size: 18)] : [Text(hint!)],
              ),
            ),
          ),
        ),
      );
    }
    return out;
  }

  Widget _topBar(BuildContext context, AppLocalizations l, MediaEntry m, bool wide) {
    final where = l.positionOf(_index + 1, widget.items.length);
    const name = TextStyle(fontWeight: FontWeight.w700);
    const muted = TextStyle(fontSize: 12, color: Colors.white70);
    return Positioned(
      key: const ValueKey('layer-top'),
      left: 0,
      right: 0,
      top: 0,
      child: DecoratedBox(
        decoration: const BoxDecoration(gradient: LinearGradient(begin: Alignment.topCenter, end: Alignment.bottomCenter, colors: [Color(0xCC000000), Color(0x00000000)])),
        child: SafeArea(
          bottom: false,
          child: Padding(
            padding: EdgeInsets.fromLTRB(4, 4, 4, wide ? 22 : 30),
            child: Row(
              children: [
                IconButton(key: const Key('player-close'), tooltip: l.closePlayer, icon: const Icon(Icons.arrow_back_rounded), onPressed: () => Navigator.of(context).maybePop()),
                Expanded(
                  child: wide
                      ? Row(
                          crossAxisAlignment: CrossAxisAlignment.baseline,
                          textBaseline: TextBaseline.alphabetic,
                          children: [
                            Flexible(child: Text(m.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: name.copyWith(fontSize: 16))),
                            const SizedBox(width: 10),
                            Text(where, style: muted),
                          ],
                        )
                      : Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            // Two lines for the name: upright, a series' episode was cut short.
                            Text(m.name, maxLines: 2, overflow: TextOverflow.ellipsis, style: name.copyWith(fontSize: 15, height: 1.25)),
                            Text(
                              [where, if (m.durationMs > 0) clock(Duration(milliseconds: m.durationMs)), formatBytes(context, m.size)].join(' · '),
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: muted,
                            ),
                          ],
                        ),
                ),
                IconButton(key: const Key('player-menu'), tooltip: l.moreOptions, icon: const Icon(Icons.more_vert_rounded), onPressed: () => _openPanel(_Panel.menu)),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _resumeToast(BuildContext context, AppLocalizations l, Color accent, Duration at) => Container(
    key: const Key('video-resume'),
    margin: const EdgeInsets.only(bottom: 10),
    padding: const EdgeInsets.only(left: 16, right: 4),
    decoration: BoxDecoration(color: const Color(0xFF1C1F1D), borderRadius: BorderRadius.circular(22)),
    child: Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(l.resumedAt(clock(at)), style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600)),
        TextButton(
          key: const Key('video-restart'),
          onPressed: () {
            _touched();
            unawaited(_player.seek(Duration.zero));
            setState(() => _resumed = null);
          },
          child: Text(l.startOver, style: TextStyle(color: accent, fontWeight: FontWeight.w800, fontSize: 13)),
        ),
      ],
    ),
  );

  Widget _commands(BuildContext context, AppLocalizations l, Color accent, MediaEntry m, bool wide) {
    final p = _player;
    final transport = [
      IconButton(
        key: const Key('player-previous'),
        tooltip: l.previousItem,
        iconSize: 30,
        icon: const Icon(Icons.skip_previous_rounded),
        onPressed: _index > 0
            ? () {
                _touched();
                unawaited(_open(_index - 1));
              }
            : null,
      ),
      SizedBox(width: wide ? 14 : 22),
      SizedBox.square(
        dimension: wide ? 56 : 64,
        child: IconButton.filled(
          key: const Key('player-play'),
          tooltip: p.playing ? l.pauseMedia : l.playMedia,
          iconSize: 30,
          style: IconButton.styleFrom(backgroundColor: accent, foregroundColor: _onAccent),
          icon: Icon(p.playing ? Icons.pause_rounded : Icons.play_arrow_rounded),
          onPressed: p.ready ? () => unawaited(_playOrPause()) : null,
        ),
      ),
      SizedBox(width: wide ? 14 : 22),
      IconButton(
        key: const Key('player-next'),
        tooltip: l.nextItem,
        iconSize: 30,
        icon: const Icon(Icons.skip_next_rounded),
        onPressed: _index < widget.items.length - 1
            ? () {
                _touched();
                unawaited(_open(_index + 1));
              }
            : null,
      ),
    ];
    final pick = _SelectPill(entry: m, accent: accent, onUsed: _touched);
    final subtitles = p.subtitleTracks.isEmpty
        ? null
        : IconButton(
            key: const Key('player-subtitles'),
            tooltip: l.subtitlesLabel,
            isSelected: p.subtitleTrack != null,
            color: Colors.white,
            selectedIcon: Icon(Icons.closed_caption_rounded, color: accent),
            icon: const Icon(Icons.closed_caption_off_outlined),
            onPressed: _toggleSubtitles,
          );
    final audio = p.audioTracks.length < 2
        ? null
        : IconButton(key: const Key('player-audio'), tooltip: l.audioTrack, icon: const Icon(Icons.graphic_eq_rounded), onPressed: () => _openPanel(_Panel.audio));
    final picture = IconButton(
      key: const Key('player-picture'),
      tooltip: _fill ? l.wholePicture : l.fillScreen,
      icon: Icon(_fill ? Icons.fit_screen_rounded : Icons.aspect_ratio_rounded),
      onPressed: () {
        _touched();
        _setPicture(_fill ? _Picture.fit : _Picture.crop);
      },
    );
    final rotate = IconButton(key: const Key('player-rotate'), tooltip: l.rotateScreen, icon: const Icon(Icons.screen_rotation_rounded), onPressed: _rotate);
    final upNext = IconButton(key: const Key('player-up-next'), tooltip: l.upNext, icon: const Icon(Icons.playlist_play_rounded), onPressed: () => _openPanel(_Panel.next));
    return DecoratedBox(
      decoration: const BoxDecoration(
        gradient: LinearGradient(begin: Alignment.bottomCenter, end: Alignment.topCenter, stops: [0, 0.55, 1], colors: [Color(0xE6000000), Color(0xB3000000), Color(0x00000000)]),
      ),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: EdgeInsets.fromLTRB(wide ? 16 : 10, wide ? 36 : 48, wide ? 16 : 10, wide ? 2 : 10),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              _seekRow(context, l, accent),
              if (wide)
                Row(
                  children: [
                    Expanded(child: Row(children: [Flexible(child: pick), ?subtitles, ?audio])),
                    ...transport,
                    Expanded(child: Row(mainAxisAlignment: MainAxisAlignment.end, children: [picture, rotate, upNext])),
                  ],
                )
              else ...[
                Row(mainAxisAlignment: MainAxisAlignment.center, children: transport),
                const SizedBox(height: 6),
                // « Sélectionner » gives way, its word cut short, before the buttons do (a large
                // text, a long word in another language).
                Row(children: [Flexible(fit: FlexFit.tight, child: Align(alignment: Alignment.centerLeft, child: pick)), ?subtitles, ?audio, rotate, upNext]),
              ],
            ],
          ),
        ),
      ),
    );
  }

  Widget _seekRow(BuildContext context, AppLocalizations l, Color accent) {
    final p = _player;
    final total = p.duration.inMilliseconds / 1000;
    final shown = _seekTo ?? p.position;
    final at = (shown.inMilliseconds / 1000).clamp(0, total > 0 ? total : 0).toDouble();
    String time(double s) => clock(Duration(seconds: s.round()));
    const style = TextStyle(fontSize: 12, fontWeight: FontWeight.w700, fontFeatures: tabular);
    return Row(
      children: [
        // The times as wide as they are, the bar between them without the slider's own margin:
        // upright, the bar was half the screen's width (real test of 24 November 2026).
        Text(time(at), style: style),
        Expanded(
          child: Semantics(
            label: l.seekLabel,
            child: SliderTheme(
              data: SliderThemeData(
                trackHeight: 3,
                padding: const EdgeInsets.symmetric(horizontal: 12),
                activeTrackColor: accent,
                inactiveTrackColor: Colors.white24,
                thumbColor: accent,
                overlayColor: accent.withValues(alpha: 0.2),
                thumbShape: const RoundSliderThumbShape(enabledThumbRadius: 7),
              ),
              child: Slider(
                key: const Key('player-seek'),
                value: at,
                max: total > 0 ? total : 1,
                onChangeStart: (_) {
                  _sliding = true;
                  _hide?.cancel();
                },
                onChanged: total > 0 ? (v) => _scrub(Duration(milliseconds: (v * 1000).round())) : null,
                onChangeEnd: (v) {
                  _sliding = false;
                  unawaited(p.seek(Duration(milliseconds: (v * 1000).round())));
                  _clearSoon();
                  _armHide();
                },
              ),
            ),
          ),
        ),
        Text(time(total), style: style),
      ],
    );
  }

  // ——— The panels: the menu ⋮ and what it opens, « À suivre » ———

  Widget _panelBox(BuildContext context, AppLocalizations l, Color accent, MediaEntry m, Size size, bool wide) {
    final content = switch (_panel) {
      _Panel.menu => _menu(context, l, m),
      _Panel.speed => _speedPanel(l, accent),
      _Panel.audio => _trackPanel(l, accent, audio: true),
      _Panel.subtitles => _trackPanel(l, accent, audio: false),
      _Panel.picture => _picturePanel(l, accent),
      _Panel.info => _infoPanel(context, l, m),
      _Panel.next => _nextPanel(context, l, accent),
      _Panel.none => const SizedBox.shrink(),
    };
    if (wide) {
      return Positioned(
        top: 0,
        bottom: 0,
        right: 0,
        width: size.width * 0.44 > 380 ? 380 : size.width * 0.44,
        child: Material(
          key: const Key('video-panel'),
          color: _panelColor,
          clipBehavior: Clip.antiAlias,
          shape: const RoundedRectangleBorder(borderRadius: BorderRadius.horizontal(left: Radius.circular(22))),
          child: SafeArea(left: false, child: content),
        ),
      );
    }
    return Positioned(
      left: 0,
      right: 0,
      bottom: 0,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxHeight: size.height * 0.72),
        child: Material(
          key: const Key('video-panel'),
          color: _panelColor,
          clipBehavior: Clip.antiAlias,
          shape: const RoundedRectangleBorder(borderRadius: BorderRadius.vertical(top: Radius.circular(22))),
          child: SafeArea(top: false, child: content),
        ),
      ),
    );
  }

  Widget _list(List<Widget> children) => ListView(shrinkWrap: true, padding: const EdgeInsets.symmetric(vertical: 8), children: children);

  Widget _header(AppLocalizations l, String title, {bool back = true, Widget? trailing}) => Padding(
    padding: const EdgeInsets.fromLTRB(4, 0, 8, 4),
    child: Row(
      children: [
        if (back) IconButton(tooltip: l.moreOptions, icon: const Icon(Icons.arrow_back_rounded), onPressed: () => setState(() => _panel = _Panel.menu)) else const SizedBox(width: 12),
        Expanded(child: Text(title, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w800))),
        ?trailing,
      ],
    ),
  );

  Widget _row({Key? key, required IconData icon, required String label, String? value, bool more = false, Widget? trailing, VoidCallback? onTap}) => ListTile(
    key: key,
    minTileHeight: 52,
    leading: Icon(icon),
    title: Text(label, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
    trailing:
        trailing ??
        Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (value != null) Text(value, style: const TextStyle(fontSize: 13, color: Colors.white70)),
            if (more) const Icon(Icons.chevron_right_rounded, color: Colors.white70),
          ],
        ),
    onTap: onTap,
  );

  Widget _choice({Key? key, required bool on, required String label, required Color accent, required VoidCallback onTap}) => ListTile(
    key: key,
    minTileHeight: 52,
    selected: on,
    selectedColor: accent,
    leading: Icon(on ? Icons.radio_button_checked_rounded : Icons.radio_button_unchecked_rounded),
    title: Text(label, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
    onTap: onTap,
  );

  Widget _menu(BuildContext context, AppLocalizations l, MediaEntry m) {
    final p = _player;
    final openWith = _state.openWith;
    return _list([
      _row(key: const Key('video-menu-speed'), icon: Icons.speed_rounded, label: l.playbackSpeed, value: _speedLabel(p.speed), more: true, onTap: () => setState(() => _panel = _Panel.speed)),
      if (p.audioTracks.length > 1)
        _row(key: const Key('video-menu-audio'), icon: Icons.graphic_eq_rounded, label: l.audioTrack, value: _trackLabel(l, p.audioTracks, p.audioTrack), more: true, onTap: () => setState(() => _panel = _Panel.audio)),
      if (p.subtitleTracks.isNotEmpty)
        _row(key: const Key('video-menu-subtitles'), icon: Icons.closed_caption_rounded, label: l.subtitlesLabel, value: _trackLabel(l, p.subtitleTracks, p.subtitleTrack), more: true, onTap: () => setState(() => _panel = _Panel.subtitles)),
      _row(key: const Key('video-menu-picture'), icon: Icons.aspect_ratio_rounded, label: l.pictureFormat, value: _pictureLabel(l, _picture), more: true, onTap: () => setState(() => _panel = _Panel.picture)),
      _row(
        key: const Key('video-menu-repeat'),
        icon: Icons.repeat_rounded,
        label: l.repeatVideo,
        trailing: Switch(value: p.looping, onChanged: (on) => unawaited(p.setLooping(on))),
        onTap: () => unawaited(p.setLooping(!p.looping)),
      ),
      _row(key: const Key('video-menu-info'), icon: Icons.info_outline_rounded, label: l.fileInfo, more: true, onTap: () => setState(() => _panel = _Panel.info)),
      if (openWith.supported) ...[
        _row(key: const Key('video-menu-open-with'), icon: Icons.open_in_new_rounded, label: l.openWithOther, onTap: () => unawaited(openWith.open(m))),
        _row(key: const Key('video-menu-share'), icon: Icons.share_rounded, label: l.shareMedia, onTap: () => unawaited(openWith.share(m))),
      ],
    ]);
  }

  Widget _speedPanel(AppLocalizations l, Color accent) {
    final p = _player;
    return _list([
      _header(l, l.playbackSpeed),
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
        child: Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final s in speeds)
              ChoiceChip(
                key: Key('video-speed-$s'),
                label: Text(_speedLabel(s), style: const TextStyle(fontWeight: FontWeight.w800)),
                selected: (p.speed - s).abs() < 0.01,
                selectedColor: accent,
                labelStyle: TextStyle(color: (p.speed - s).abs() < 0.01 ? _onAccent : Colors.white),
                onSelected: (_) => unawaited(p.setSpeed(s)),
              ),
          ],
        ),
      ),
      Padding(padding: const EdgeInsets.fromLTRB(16, 4, 16, 8), child: Text(l.holdForDouble, style: const TextStyle(fontSize: 12, color: Colors.white70, height: 1.4))),
    ]);
  }

  Widget _trackPanel(AppLocalizations l, Color accent, {required bool audio}) {
    final p = _player;
    final tracks = audio ? p.audioTracks : p.subtitleTracks;
    final now = audio ? p.audioTrack : p.subtitleTrack;
    return _list([
      _header(l, audio ? l.audioTrack : l.subtitlesLabel),
      if (!audio) _choice(key: const Key('video-subtitles-off'), on: now == null, label: l.subtitlesOff, accent: accent, onTap: () => unawaited(p.setSubtitleTrack(null))),
      for (final (i, t) in tracks.indexed)
        _choice(
          key: Key('video-track-${t.id}'),
          on: t.id == now,
          label: t.label.isEmpty ? l.trackNumber(i + 1) : t.label,
          accent: accent,
          onTap: () => unawaited(audio ? p.setAudioTrack(t.id) : p.setSubtitleTrack(t.id)),
        ),
    ]);
  }

  Widget _picturePanel(AppLocalizations l, Color accent) => _list([
    _header(l, l.pictureFormat),
    for (final p in _Picture.values)
      _choice(key: Key('video-picture-${p.name}'), on: _picture == p, label: _pictureLabel(l, p), accent: accent, onTap: () => _setPicture(p)),
    ListenableBuilder(
      listenable: _state.settings,
      builder: (context, _) => SwitchListTile(
        key: const Key('video-under-notch'),
        value: _state.settings.videoUnderNotch,
        title: Text(l.underNotch, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
        onChanged: (on) {
          _state.settings.videoUnderNotch = on;
          unawaited(_state.screen.setUnderNotch(on));
        },
      ),
    ),
  ]);

  Widget _infoPanel(BuildContext context, AppLocalizations l, MediaEntry m) {
    final engine = _player.engine;
    const muted = TextStyle(fontSize: 13, color: Colors.white70, height: 1.4);
    return _list([
      _header(l, l.fileInfo),
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
        child: Column(
          key: const Key('video-info'),
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(m.name, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700)),
            const SizedBox(height: 6),
            Text(mediaDetails(context, m), style: muted),
            const SizedBox(height: 10),
            Text(l.fileLocation, style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Colors.white70)),
            Text(m.path, style: muted),
            if (engine != null) ...[const SizedBox(height: 10), Text(l.playedBy(engine), style: muted)],
          ],
        ),
      ),
    ]);
  }

  Widget _nextPanel(BuildContext context, AppLocalizations l, Color accent) {
    final settings = _state.settings;
    final items = widget.items;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 8, 0),
          child: Row(
            children: [
              Expanded(child: Text(l.upNext, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w800))),
              Text(l.autoplayNext, style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w700, color: Colors.white70)),
              ListenableBuilder(
                listenable: settings,
                builder: (context, _) => Switch(key: const Key('video-autoplay'), value: settings.videoAutoplay, onChanged: (on) => settings.videoAutoplay = on),
              ),
            ],
          ),
        ),
        Flexible(
          child: ListView.builder(
            shrinkWrap: true,
            padding: const EdgeInsets.fromLTRB(8, 4, 8, 8),
            itemCount: items.length - _index,
            itemBuilder: (context, k) {
              final i = _index + k;
              final v = items[i];
              final now = i == _index;
              return ListTile(
                key: ValueKey('up-next-${v.id}'),
                minTileHeight: 68,
                selected: now,
                selectedTileColor: const Color(0x12FFFFFF),
                shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                onTap: now
                    ? null
                    : () {
                        setState(() => _panel = _Panel.none);
                        unawaited(_open(i));
                      },
                leading: ClipRRect(
                  borderRadius: BorderRadius.circular(8),
                  child: SizedBox(
                    width: 96,
                    height: 54,
                    child: Stack(
                      fit: StackFit.expand,
                      children: [
                        LibraryImage(cacheKey: 'video:${v.id}', load: (px) => _state.library.thumbnail(MediaKind.video, v.id, px)),
                        if (v.durationMs > 0)
                          Positioned(
                            right: 4,
                            bottom: 4,
                            child: Container(
                              padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                              decoration: BoxDecoration(color: const Color(0xBF000000), borderRadius: BorderRadius.circular(5)),
                              child: Text(clock(Duration(milliseconds: v.durationMs)), style: const TextStyle(fontSize: 11, fontWeight: FontWeight.w700, color: Colors.white)),
                            ),
                          ),
                      ],
                    ),
                  ),
                ),
                title: Text(v.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700, color: Colors.white)),
                subtitle: Text(
                  now ? l.nowPlaying : formatBytes(context, v.size),
                  style: TextStyle(fontSize: 12, color: now ? accent : Colors.white70, fontWeight: now ? FontWeight.w800 : FontWeight.w500),
                ),
              );
            },
          ),
        ),
      ],
    );
  }
}

/// The subtitles, on a dark band, over the picture.
class _Caption extends StatelessWidget {
  const _Caption({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) => IgnorePointer(
    child: Padding(
      padding: const EdgeInsets.fromLTRB(24, 0, 24, 12),
      child: Container(
        key: const Key('video-subtitle'),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
        decoration: BoxDecoration(color: const Color(0xA0000000), borderRadius: BorderRadius.circular(6)),
        child: Text(text, textAlign: TextAlign.center, style: const TextStyle(color: Colors.white, fontSize: 16, fontWeight: FontWeight.w600, height: 1.35)),
      ),
    ),
  );
}

/// A word of what a gesture did, at the top of the picture.
class _Pill extends StatelessWidget {
  const _Pill({super.key, required this.children});

  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Container(
    constraints: const BoxConstraints(minHeight: 36),
    padding: const EdgeInsets.symmetric(horizontal: 16),
    decoration: BoxDecoration(color: const Color(0xE0151816), borderRadius: BorderRadius.circular(18)),
    child: DefaultTextStyle.merge(
      style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w800, color: Colors.white),
      child: Row(mainAxisSize: MainAxisSize.min, children: children),
    ),
  );
}

/// « Sélectionner »: the grid's selection, at hand in the player's commands.
class _SelectPill extends StatelessWidget {
  const _SelectPill({required this.entry, required this.accent, required this.onUsed});

  final MediaEntry entry;
  final Color accent;
  final VoidCallback onUsed;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final sel = AppScope.of(context).state.selection;
    final pick = Explorer.pickMedia(entry);
    return ListenableBuilder(
      listenable: sel,
      builder: (context, _) {
        final on = sel.contains(pick.path);
        final shape = StadiumBorder(side: BorderSide(color: on ? accent : Colors.white54, width: 2));
        return Semantics(
          button: true,
          toggled: on,
          child: Material(
            color: on ? accent.withValues(alpha: 0.15) : const Color(0x59000000),
            shape: shape,
            child: InkWell(
              key: const Key('media-select'),
              customBorder: shape,
              onTap: () {
                sel.toggle(pick);
                onUsed();
              },
              child: ConstrainedBox(
                constraints: const BoxConstraints(minHeight: 44),
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(12, 0, 16, 0),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      PickBox(on: on, onImage: true, size: 20),
                      const SizedBox(width: 8),
                      Flexible(
                        child: Text(on ? l.selectedShort : l.selectThis, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 13)),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}
