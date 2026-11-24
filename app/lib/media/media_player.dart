/// Playing a video or a song (plan J8, WP-8.2, decision D8): the screens know only
/// [MediaPlayer]; each system has its adapter, and another can replace it without touching them.
library;

import 'package:flutter/widgets.dart';

import '../platform/library.dart';

/// One player: a video or a song at a time, opened again for the next.
abstract class MediaPlayer extends ChangeNotifier {
  /// Opens [m], read where it is (Android: its `content://` address, nothing copied, STO-16).
  /// A format the system does not read leaves [error] set.
  Future<void> open(MediaEntry m);

  bool get ready;
  bool get playing;

  /// Played to its end.
  bool get finished;
  Duration get position;
  Duration get duration;

  /// From 0 to 1.
  double get volume;

  /// The picture's width over its height; 0 for a song, or before it is known.
  double get aspect;

  /// Why it does not play; null while it does.
  String? get error;

  Future<void> play();
  Future<void> pause();
  Future<void> seek(Duration to);
  Future<void> setVolume(double volume);

  /// The picture, for a video, fitted in the space it is given: whole ([BoxFit.contain]), or
  /// filling it ([BoxFit.cover], « Remplir l'écran »).
  Widget view({BoxFit fit = BoxFit.contain});

  /// How fast it plays: 1 is its own speed.
  double get speed => 1;
  Future<void> setSpeed(double speed) async {}

  /// Played again from its start once at its end (« Répéter la vidéo »).
  bool get looping => false;
  Future<void> setLooping(bool on) async {}

  /// The sound tracks and the subtitles the file carries, where the engine reads them; none
  /// otherwise (the screens then offer no choice).
  List<MediaTrack> get audioTracks => const [];
  List<MediaTrack> get subtitleTracks => const [];

  /// The sound track played, and the subtitles shown (null: none), by their [MediaTrack.id].
  String? get audioTrack => null;
  String? get subtitleTrack => null;
  Future<void> setAudioTrack(String id) async {}
  Future<void> setSubtitleTrack(String? id) async {}

  /// The subtitles' words at this moment: empty when none show.
  String get subtitle => '';

  /// The engine playing it, named in the file's details; null where there is a single one.
  String? get engine => null;
}

/// A sound track or the subtitles of a file: its id for the engine, and what it is called.
class MediaTrack {
  const MediaTrack(this.id, this.label);

  final String id;
  final String label;
}

/// Makes the players of this system.
abstract interface class MediaPlayers {
  /// False where no adapter plays yet: the screens say so.
  bool get supported;

  MediaPlayer create();
}

/// Where nothing plays (yet): a player that says so.
class NoMediaPlayers implements MediaPlayers {
  const NoMediaPlayers();

  @override
  bool get supported => false;

  @override
  MediaPlayer create() => _Unsupported();
}

class _Unsupported extends MediaPlayer {
  @override
  Future<void> open(MediaEntry m) async => notifyListeners();
  @override
  bool get ready => false;
  @override
  bool get playing => false;
  @override
  bool get finished => false;
  @override
  Duration get position => Duration.zero;
  @override
  Duration get duration => Duration.zero;
  @override
  double get volume => 1;
  @override
  double get aspect => 0;
  @override
  String? get error => 'unsupported';
  @override
  Future<void> play() async {}
  @override
  Future<void> pause() async {}
  @override
  Future<void> seek(Duration to) async {}
  @override
  Future<void> setVolume(double volume) async {}
  @override
  Widget view({BoxFit fit = BoxFit.contain}) => const SizedBox.shrink();
}

/// A media's address in Android's MediaStore, which the players and other apps read.
Uri contentUriOf(MediaEntry m) => Uri.parse('content://media/external/${switch (m.kind) {
  MediaKind.images => 'images',
  MediaKind.video => 'video',
  MediaKind.audio => 'audio',
}}/media/${m.id}');

/// Its kind for an app chooser.
String mimeOf(MediaEntry m) => switch (m.kind) {
  MediaKind.images => 'image/*',
  MediaKind.video => 'video/*',
  MediaKind.audio => 'audio/*',
};
