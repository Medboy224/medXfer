// The explorer by categories (WP-B.9) on a fake library: tabs, picking across tabs, the
// basket, select all, and how the selection goes to the core.
import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/features/explorer/explorer_tabs.dart' show gridDecodePx;
import 'package:medxfer/features/explorer/gallery_toolbar.dart';
import 'package:medxfer/library/explorer.dart';
import 'package:medxfer/library/selection.dart';
import 'package:medxfer/media/media_player.dart';
import 'package:medxfer/media/pdf_document.dart';
import 'package:medxfer/media/two_engines.dart';
import 'package:medxfer/media/video_player_adapter.dart' show shownSize;
import 'package:medxfer/platform/open_with.dart';
import 'package:medxfer/platform/app_exit.dart';
import 'package:medxfer/platform/screen_controls.dart';
import 'package:medxfer/platform/file_picker.dart';
import 'package:medxfer/platform/folders.dart';
import 'package:medxfer/widgets/v4.dart';
import 'package:medxfer/platform/library.dart';
import 'package:medxfer/platform/storage.dart';
import 'package:medxfer/settings.dart';
import 'package:medxfer/theme.dart';
import 'package:medxfer/transfer/history.dart';

class _TestFolders implements Folders {
  const _TestFolders();
  @override
  Future<String> dataDir() async => '/tmp/medxfer-test/data';
  @override
  Future<String> downloadDir() async => '/tmp/medxfer-test/out';
}

class _Storage extends NoReceiveStorage {
  const _Storage();
  @override
  bool get supported => true;
}

/// A 1×1 PNG: a thumbnail that decodes.
final _png = base64Decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==');

/// A phone with two apps, three photos, a song, and a folder.
class _Library implements Library {
  _Library({this.photos = 3, this.long = false, this.sizes = const [], this.videos = 0, this.songs = 1, this.thumbs = false});

  /// How many videos and songs the phone holds.
  final int videos, songs;

  /// Its thumbnails: each its own bytes (none otherwise).
  final bool thumbs;

  /// The photos' sizes (width, height), from the first; unknown beyond.
  final List<(int, int)> sizes;

  /// How many photos the phone holds.
  int photos;

  /// Says the device's content changed, as Library.kt does.
  final changed = StreamController<void>.broadcast();
  @override
  Stream<void> get changes => changed.stream;

  /// With a document and a photo under a very long name.
  final bool long;
  bool allowed = true;
  final opened = <List<({String path, String name})>>[];

  /// The orders asked, kind by kind: done by the platform (WP-8.1b).
  final orders = <(MediaKind, MediaOrder)>[];

  @override
  bool get supported => true;
  @override
  Future<List<AppEntry>> apps() async => const [
    AppEntry(package: 'org.notes', label: 'Notes', path: '/data/app/org.notes/base.apk', size: 4 << 20),
    AppEntry(package: 'com.maps', label: 'Cartes', path: '/data/app/com.maps/base.apk', size: 30 << 20, split: true),
  ];
  @override
  Future<Uint8List?> appIcon(String package, int size) async => null;
  @override
  Future<List<MediaEntry>> media(MediaKind kind, {int offset = 0, int limit = 200, MediaOrder order = const MediaOrder()}) async {
    if (offset == 0) orders.add((kind, order));
    return _media(kind, offset, order);
  }

  List<MediaEntry> _media(MediaKind kind, int offset, MediaOrder order) => offset > 0 || (order.search.isNotEmpty && order.search != 'IMG_2')
      ? const []
      : switch (kind) {
          MediaKind.images => [
            for (var i = 1; i <= photos; i++)
              MediaEntry(
                id: i,
                kind: kind,
                name: long && i == 3 ? _longName : 'IMG_$i.jpg',
                path: '/storage/emulated/0/DCIM/Camera/IMG_$i.jpg',
                size: 1 << 20,
                album: i < 3 ? 'Camera' : 'WhatsApp',
                width: i <= sizes.length ? sizes[i - 1].$1 : 0,
                height: i <= sizes.length ? sizes[i - 1].$2 : 0,
              ),
          ],
          MediaKind.audio => [
            const MediaEntry(id: 9, kind: MediaKind.audio, name: 'chanson.mp3', path: '/storage/emulated/0/Music/chanson.mp3', size: 5 << 20, album: 'Album', durationMs: 200000),
            for (var i = 2; i <= songs; i++)
              MediaEntry(id: 9 + i, kind: MediaKind.audio, name: 'chanson-$i.mp3', path: '/storage/emulated/0/Music/chanson-$i.mp3', size: 5 << 20, durationMs: 180000),
          ],
          MediaKind.video => [
            for (var i = 1; i <= videos; i++)
              MediaEntry(id: 100 + i, kind: MediaKind.video, name: 'film-$i.mp4', path: '/storage/emulated/0/DCIM/film-$i.mp4', size: 50 << 20, durationMs: 60000, width: 1920, height: 1080),
          ],
        };
  @override
  Future<Uint8List?> thumbnail(MediaKind kind, int id, int size) async => thumbs ? Uint8List.fromList(_png) : null;

  /// Asked by path: the photos the library knows (those of the fake folder « DCIM »).
  final mediaAsked = <List<String>>[];
  @override
  Future<List<MediaEntry>> mediaAt(MediaKind kind, List<String> paths) async {
    mediaAsked.add(paths);
    return [
      for (final p in paths)
        if (p.endsWith('known.jpg')) MediaEntry(id: 77, kind: kind, name: 'known.jpg', path: p, size: 1),
    ];
  }

  /// The sharp images asked: (id, px).
  final sharpAsked = <(int, int)>[];

  /// The sharp images asked only if made already: ids.
  final keptAsked = <int>[];
  /// The preparations asked: (height, width, on).
  final prepared = <(int, int, bool)>[];
  @override
  Future<void> prepareSharp({required int height, required int width, required bool on}) async => prepared.add((height, width, on));
  @override
  Future<Uint8List?> sharpThumbnail(MediaEntry m, {required int height, required int width, bool keptOnly = false}) async {
    if (keptOnly) {
      keptAsked.add(m.id);
      return null; // none made yet
    }
    sharpAsked.add((m.id, height));
    return null;
  }
  @override
  Future<bool> mediaAllowed() async => allowed;
  @override
  Future<bool> askMedia() async => allowed = true;
  @override
  String get storageRoot => '/storage/emulated/0';
  @override
  Future<List<FileEntry>> listDir(String path) async => path == storageRoot
      ? const [
          FileEntry(name: 'DCIM', path: '/storage/emulated/0/DCIM', dir: true, size: 0),
          FileEntry(name: 'Camera', path: '/storage/emulated/0/DCIM/Camera', dir: true, size: 0),
        ]
      : path.endsWith('/Camera')
      ? const [
          FileEntry(name: 'IMG_1.jpg', path: '/storage/emulated/0/DCIM/Camera/IMG_1.jpg', dir: false, size: 10),
          FileEntry(name: 'shot.heic', path: '/storage/emulated/0/DCIM/Camera/shot.heic', dir: false, size: 20),
        ]
      : path.endsWith('/DCIM')
      ? const [
          FileEntry(name: 'known.jpg', path: '/storage/emulated/0/DCIM/known.jpg', dir: false, size: 10),
          FileEntry(name: 'clip.mp4', path: '/storage/emulated/0/DCIM/clip.mp4', dir: false, size: 20),
          FileEntry(name: 'other.jpg', path: '/storage/emulated/0/DCIM/other.jpg', dir: false, size: 30),
          FileEntry(name: 'notes.txt', path: '/storage/emulated/0/DCIM/notes.txt', dir: false, size: 40),
        ]
      : const [];
  @override
  Future<Uint8List?> fileThumbnail(String path, int px) async => null;

  /// The photos decoded apart: their paths.
  final decodedPhotos = <String>[];
  @override
  Future<Uint8List?> decodePhoto(String path, int width, int height) async {
    decodedPhotos.add(path);
    return null;
  }
  @override
  Future<int> dirSize(String path) async => 7 << 20;
  @override
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200}) async => offset > 0
      ? const []
      : switch (category) {
          FileCategory.documents => [
            const FileEntry(name: 'rapport.pdf', path: '/storage/emulated/0/Documents/rapport.pdf', dir: false, size: 2 << 20),
            if (long) const FileEntry(name: _longName, path: '/storage/emulated/0/Documents/$_longName', dir: false, size: 2 << 20),
          ],
          _ => const [],
        };
  @override
  Future<List<OpenFile>> open(List<({String path, String name})> files) async {
    opened.add(files);
    return [for (var i = 0; i < files.length; i++) OpenFile(fd: 100 + i, name: files[i].name)];
  }
}

const _longName = '6A41A353-7F9F-4A54-A1A1-FCAxngxhkdykduodoudoudiyydyis7soydyosyidyodyodoysyisoyaitsoysiysiysoysyoskysyis6iskys6o.png';

/// How the app left, by the back button.
class _Exits implements AppExit {
  final calls = <String>[];
  @override
  Future<void> toBack() async => calls.add('toBack');
  @override
  Future<void> quit() async => calls.add('quit');
}

_Exits _watchExits() {
  final exits = _Exits();
  appExit = exits;
  addTearDown(() => appExit = const SystemExit());
  return exits;
}

/// A player that remembers what it was asked; [fail] makes every one refuse its media. [tracks]:
/// each file carries two sound tracks and subtitles; [picture] false: a video plays without it.
class _FakePlayer extends MediaPlayer {
  _FakePlayer(this.fail, {this.tracks = false, this.picture = true, this.name = 'fake'});

  final String? fail;
  final bool tracks, picture;
  final String name;
  final opened = <int>[];
  bool _ready = false, _playing = false, _finished = false, _looping = false, disposed = false;
  double _volume = 1, _speed = 1;
  Duration _position = Duration.zero;
  String? _audio, _subtitles;

  /// How the picture was last asked to fit.
  BoxFit? fit;

  String? _late;

  /// Fails once open, as it starts to play.
  void breakDown(String why) {
    _late = why;
    _playing = false;
    notifyListeners();
  }

  /// Plays it to its end.
  void finish() {
    _finished = true;
    _playing = false;
    notifyListeners();
  }

  @override
  Future<void> open(MediaEntry m) async {
    opened.add(m.id);
    _ready = fail == null;
    _playing = false;
    _finished = false;
    _position = Duration.zero;
    _audio = tracks ? 'a1' : null;
    _subtitles = null;
    notifyListeners();
  }

  @override
  double get speed => _speed;
  @override
  Future<void> setSpeed(double speed) async {
    _speed = speed;
    notifyListeners();
  }

  @override
  bool get looping => _looping;
  @override
  Future<void> setLooping(bool on) async {
    _looping = on;
    notifyListeners();
  }

  @override
  List<MediaTrack> get audioTracks => tracks ? const [MediaTrack('a1', 'Français'), MediaTrack('a2', 'English')] : const [];
  @override
  List<MediaTrack> get subtitleTracks => tracks ? const [MediaTrack('s1', 'Français')] : const [];
  @override
  String? get audioTrack => _audio;
  @override
  String? get subtitleTrack => _subtitles;
  @override
  Future<void> setAudioTrack(String id) async {
    _audio = id;
    notifyListeners();
  }

  @override
  Future<void> setSubtitleTrack(String? id) async {
    _subtitles = id;
    notifyListeners();
  }

  @override
  String get subtitle => _subtitles == null ? '' : 'Bonjour.';
  @override
  String get engine => name;

  @override
  void dispose() {
    disposed = true;
    super.dispose();
  }

  @override
  bool get ready => _ready;
  @override
  bool get playing => _playing;
  @override
  bool get finished => _finished;
  @override
  Duration get position => _position;
  @override
  Duration get duration => const Duration(minutes: 1);
  @override
  double get volume => _volume;
  @override
  double get aspect => picture ? 16 / 9 : 0;
  @override
  String? get error => fail ?? _late;
  @override
  Future<void> play() async {
    _playing = true;
    notifyListeners();
  }

  @override
  Future<void> pause() async {
    _playing = false;
    notifyListeners();
  }

  @override
  Future<void> seek(Duration to) async {
    _position = to;
    notifyListeners();
  }

  @override
  Future<void> setVolume(double volume) async {
    _volume = volume;
    notifyListeners();
  }

  @override
  Widget view({BoxFit fit = BoxFit.contain}) {
    this.fit = fit;
    return const ColoredBox(color: Colors.blue);
  }
}

class _FakePlayers implements MediaPlayers {
  _FakePlayers({this.fail, this.tracks = false});

  final String? fail;
  final bool tracks;
  final made = <_FakePlayer>[];

  @override
  bool get supported => true;

  @override
  MediaPlayer create() => _FakePlayer(fail, tracks: tracks)..also(made.add);
}

/// The brightness and the media volume, as the player's gestures set them.
class _FakeScreen implements ScreenControls {
  final brightnessSet = <double?>[];
  final volumeSet = <double>[];

  @override
  bool get supported => true;
  @override
  Future<double?> brightness() async => 0.5;
  @override
  Future<void> setBrightness(double? value) async => brightnessSet.add(value);
  @override
  Future<double?> volume() async => 0.4;
  @override
  Future<void> setVolume(double value) async => volumeSet.add(value);
  @override
  Future<void> setUnderNotch(bool? under) async {}
}

extension<T> on T {
  T also(void Function(T) f) {
    f(this);
    return this;
  }
}

/// The Videos tab, then [id]'s player: its fake player, playing.
Future<_FakePlayer> _openVideo(WidgetTester tester, _FakePlayers players, {int id = 101}) async {
  await tester.ensureVisible(find.text('Vidéos'));
  await tester.pumpAndSettle();
  await tester.tap(find.text('Vidéos'));
  await tester.pumpAndSettle();
  await tester.tap(find.byKey(ValueKey('open-$id')));
  await tester.pumpAndSettle();
  return players.made.last;
}

/// A PDF of 24 A4 pages; [locked]: protected by a password.
class _FakePdf implements PdfReader {
  _FakePdf({this.locked = false});

  static const pages = 24;
  final bool locked;
  final docs = <_FakePdfDoc>[];

  @override
  bool get supported => true;

  @override
  Future<PdfDocument> open(String path) async {
    if (locked) throw const PdfProtected();
    final d = _FakePdfDoc(List.filled(pages, const ui.Size(595, 842)));
    docs.add(d);
    return d;
  }
}

class _FakePdfDoc implements PdfDocument {
  _FakePdfDoc(this.pages);

  @override
  final List<ui.Size> pages;

  /// The renders asked: (page, width, region).
  final asked = <(int, int, ui.Rect?)>[];
  bool closed = false;

  @override
  Future<ui.Image?> render(int index, int width, {ui.Rect? region}) async {
    asked.add((index, width, region));
    return null; // a white page
  }

  @override
  void close() => closed = true;
}

Future<_FakePdfDoc> _openPdf(WidgetTester tester, _FakePdf pdf) async {
  await tester.ensureVisible(find.text('Fichiers'));
  await tester.pumpAndSettle();
  await tester.tap(find.text('Fichiers'));
  await tester.pumpAndSettle();
  await tester.tap(find.text('Documents'));
  await tester.pumpAndSettle();
  await tester.tap(find.byKey(const ValueKey('play-rapport.pdf')));
  await tester.pumpAndSettle();
  return pdf.docs.isEmpty ? _FakePdfDoc(const []) : pdf.docs.last;
}

class _FakeOpenWith implements OpenWith {
  final opened = <int>[];

  @override
  bool get supported => true;

  @override
  Future<bool> open(MediaEntry m) async {
    opened.add(m.id);
    return true;
  }

  final shared = <int>[];
  @override
  Future<bool> share(MediaEntry m) async {
    shared.add(m.id);
    return true;
  }

  /// Files opened or shared by path.
  final files = <String>[];
  @override
  Future<bool> openFile(String path, String mime) async {
    files.add('open $path $mime');
    return true;
  }

  @override
  Future<bool> shareFile(String path, String mime) async {
    files.add('share $path $mime');
    return true;
  }
}

Future<(FakeCore, AppState)> _app(
  WidgetTester tester,
  _Library library, {
  MediaPlayers players = const NoMediaPlayers(),
  OpenWith openWith = const NoOpenWith(),
  ScreenControls screen = const NoScreenControls(),
  PdfReader pdf = const NoPdfReader(),
}) async {
  final core = FakeCore()..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'Galaxy'})]));
  final state = AppState(
    core: core,
    folders: const _TestFolders(),
    library: library,
    storage: const _Storage(),
    history: MemoryHistoryStore(),
    players: players,
    openWith: openWith,
    screen: screen,
    pdf: pdf,
  );
  tester.platformDispatcher.localesTestValue = [const Locale('fr')];
  addTearDown(tester.platformDispatcher.clearLocalesTestValue);
  tester.view.physicalSize = const Size(1080, 2400);
  tester.view.devicePixelRatio = 2.75;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MedxferApp(state: state, settings: AppSettings()));
  await state.start();
  await tester.pumpAndSettle();
  return (core, state);
}

void main() {
  testWidgets('picks across tabs fill the basket; connected, they go by descriptor under their names', (tester) async {
    final library = _Library();
    final (core, state) = await _app(tester, library);
    core.on('send', (_) => const FakeAnswer());

    // Apps is the first tab shown.
    expect(find.text('Notes'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('app-org.notes')));
    await tester.pump();
    expect(find.text('1 élément'), findsOneWidget);
    expect(find.text('4 Mo'), findsWidgets);

    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('media-2')));
    await tester.pump();
    expect(find.text('2 éléments'), findsOneWidget);
    expect(find.text('5 Mo'), findsWidgets);

    // Not connected: the basket's button connects first.
    expect(find.text('Connecter'), findsWidgets);

    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'PC'}));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('basket-send')));
    await tester.pumpAndSettle();
    expect(library.opened.single, [
      (path: '/data/app/org.notes/base.apk', name: 'Notes.apk'),
      (path: '/storage/emulated/0/DCIM/Camera/IMG_2.jpg', name: 'IMG_2.jpg'),
    ]);
    expect(core.calls.last.payload, {
      'items': [
        {'fd': 100, 'name': 'Notes.apk'},
        {'fd': 101, 'name': 'IMG_2.jpg'},
      ],
    });
    expect(find.byKey(const Key('basket')), findsNothing, reason: 'the selection is emptied once sent');
  });

  testWidgets('select all picks the tab on screen; again, unpicks it', (tester) async {
    final (_, state) = await _app(tester, _Library());
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('select-all')));
    await tester.pump();
    expect(state.selection.count, 3);
    expect(find.text('3 éléments'), findsOneWidget);
    expect(find.text('3 Mo'), findsWidgets);
    await tester.tap(find.byKey(const Key('select-all')));
    await tester.pump();
    expect(state.selection.isEmpty, isTrue);
  });

  testWidgets('photos by folder', (tester) async {
    await _app(tester, _Library());
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('media-3')), findsOneWidget);
    await tester.tap(find.widgetWithText(ChoicePill, 'Camera'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('media-3')), findsNothing);
    expect(find.byKey(const ValueKey('media-1')), findsOneWidget);
  });

  testWidgets('without the media permission: asked in the tab', (tester) async {
    final library = _Library()..allowed = false;
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('media-allow')), findsOneWidget);
    await tester.tap(find.byKey(const Key('media-allow')));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('media-1')), findsOneWidget);
  });

  testWidgets('WP-8.1b: search, sort and layout ask the platform, from the start of the list', (tester) async {
    final library = _Library();
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    expect(find.text("Date · Récent d'abord"), findsOneWidget);

    await tester.tap(find.byKey(const Key('gallery-search-open')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('gallery-search')), 'IMG_2');
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pumpAndSettle();
    expect(library.orders.last, (MediaKind.images, const MediaOrder(search: 'IMG_2')));
    await tester.tap(find.byKey(const Key('gallery-search-close')));
    await tester.pumpAndSettle();
    expect(library.orders.last.$2.search, '');

    await tester.tap(find.byKey(const Key('gallery-sort')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('sort-name')));
    await tester.pumpAndSettle();
    expect(library.orders.last, (MediaKind.images, const MediaOrder(sort: MediaSort.name, descending: false)));
    await tester.tap(find.byKey(const Key('order-second')));
    await tester.pumpAndSettle();
    expect(library.orders.last.$2.descending, isTrue);
    await tester.tapAt(const Offset(20, 20)); // the sheet closed
    await tester.pumpAndSettle();
    expect(find.text('Nom · Z → A'), findsOneWidget);

    await tester.tap(find.byKey(const Key('gallery-layout')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('layout-5')));
    await tester.pumpAndSettle();
    final ex = AppScope.of(tester.element(find.byType(Scaffold).first), listen: false).state.explorer;
    expect((ex.columns(MediaKind.images), ex.asList(MediaKind.images)), (5, false));
  });

  testWidgets('WP-8.1b: scrolling down folds the top away and hides the bar; up, both come back; a pick keeps the bar', (tester) async {
    final library = _Library(photos: 90);
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    // How much of the top line shows: it folds with the finger, then ends its move (22 November).
    expect(find.byType(GalleryToolbar), findsOneWidget);
    double shown() => tester.widget<SizeTransition>(find.ancestor(of: find.byKey(const Key('home-menu')), matching: find.byType(SizeTransition)).first).sizeFactor.value;
    expect(shown(), 1);
    final grid = find.byKey(const ValueKey('media-images'));
    await tester.drag(grid, const Offset(0, -40));
    await tester.pumpAndSettle();
    expect(shown() == 0 || shown() == 1, isTrue, reason: 'the finger lifted, it ends its move: ${shown()}');
    await tester.drag(grid, const Offset(0, -200));
    await tester.pumpAndSettle();
    expect(shown(), 0, reason: 'the top line stays when scrolling down');
    expect(tester.widget<IgnorePointer>(find.ancestor(of: find.byKey(const Key('bottom-bar')), matching: find.byType(IgnorePointer)).first).ignoring, isTrue);
    // Down a little: the list scrolls back, the bars wait (more room without them).
    await tester.drag(grid, const Offset(0, 60));
    await tester.pumpAndSettle();
    expect(shown(), 0, reason: 'the bars came back at a short move down');
    await tester.drag(grid, const Offset(0, 400));
    await tester.pumpAndSettle();
    expect(shown(), 1);

    await tester.tap(find.byKey(const ValueKey('media-1')));
    await tester.drag(grid, const Offset(0, -200));
    await tester.pumpAndSettle();
    expect(tester.widget<IgnorePointer>(find.ancestor(of: find.byKey(const Key('bottom-bar')), matching: find.byType(IgnorePointer)).first).ignoring, isFalse,
        reason: 'the basket stays while something is picked');
  });

  testWidgets('WP-8.1b: two fingers spread the grid to fewer columns, pinch it back; the list does not move meanwhile', (tester) async {
    final library = _Library(photos: 90);
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    final ex = AppScope.of(tester.element(find.byType(Scaffold).first), listen: false).state.explorer;
    expect(ex.columns(MediaKind.images), 3);
    final grid = find.byKey(const ValueKey('media-images'));
    final center = tester.getCenter(grid);
    final scroll = tester.state<ScrollableState>(find.descendant(of: grid, matching: find.byType(Scrollable)).first).position;
    final before = scroll.pixels;

    // Spread: 3 → 2 columns.
    var a = await tester.startGesture(center - const Offset(40, 0));
    var b = await tester.startGesture(center + const Offset(40, 0), pointer: 2);
    await tester.pump();
    for (var i = 1; i <= 10; i++) {
      await a.moveTo(center - Offset(40.0 + i * 8, 0));
      await b.moveTo(center + Offset(40.0 + i * 8, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    expect(scroll.pixels, before, reason: 'the two fingers scrolled the list');
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    expect(ex.columns(MediaKind.images), 2);

    // Pinch: 2 → 3 → 4 in one gesture.
    a = await tester.startGesture(center - const Offset(150, 0));
    b = await tester.startGesture(center + const Offset(150, 0), pointer: 4);
    await tester.pump();
    for (var i = 1; i <= 14; i++) {
      await a.moveTo(center - Offset(150.0 - i * 10, 0));
      await b.moveTo(center + Offset(150.0 - i * 10, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    expect(ex.columns(MediaKind.images), greaterThanOrEqualTo(4));
  });

  testWidgets('real test of 17 November: after a step, the photo under the fingers is the same; at 2 columns the view does not zoom', (tester) async {
    final library = _Library(photos: 90);
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    final grid = find.byKey(const ValueKey('media-images')).first;
    await tester.drag(grid, const Offset(0, -600));
    await tester.pumpAndSettle();
    // The photo under a point (hit-testing, as a finger would).
    String underAt(Offset at) {
      final hits = tester.hitTestOnBinding(at).path.map((e) => e.target).toList();
      for (final key in tester.allWidgets.whereType<GestureDetector>().map((w) => w.key).whereType<ValueKey<String>>()) {
        final el = find.byKey(key).evaluate();
        if (el.isEmpty) continue;
        final box = el.first.renderObject;
        if (key.value.startsWith('media-') && hits.contains(box)) return key.value;
      }
      return '';
    }
    // The point between the fingers: on a photo, not on a day's name or a gap.
    var focal = tester.getCenter(grid);
    for (var dy = 0.0; dy < 200 && underAt(focal).isEmpty; dy += 10) {
      focal = tester.getCenter(grid) + Offset(0, dy);
    }
    String under() => underAt(focal);
    final before = under();
    expect(before, isNotEmpty);
    Future<void> spread(double from, double to) async {
      final a = await tester.startGesture(focal - Offset(from, 0));
      final b = await tester.startGesture(focal + Offset(from, 0), pointer: 7);
      await tester.pump();
      for (var i = 1; i <= 12; i++) {
        final d = from + (to - from) * i / 12;
        await a.moveTo(focal - Offset(d, 0));
        await b.moveTo(focal + Offset(d, 0));
        await tester.pump(const Duration(milliseconds: 16));
      }
      await a.up();
      await b.up();
      await tester.pumpAndSettle();
    }
    await spread(60, 100); // 3 → 2: past 1.5 times
    final ex = AppScope.of(tester.element(find.byType(Scaffold).first), listen: false).state.explorer;
    expect(ex.columns(MediaKind.images), 2);
    // A column count changes where a photo sits across; its height stays under the fingers.
    final after = tester.getRect(find.byKey(ValueKey(before)).first);
    expect((after.center.dy - focal.dy).abs(), lessThan(after.height * 0.75), reason: 'the photo left the fingers: $after, focal $focal');

    // At 2 columns, spreading more: nothing further, the same photo under the fingers.
    final a = await tester.startGesture(focal - const Offset(60, 0));
    final b = await tester.startGesture(focal + const Offset(60, 0), pointer: 9);
    await tester.pump();
    await a.moveTo(focal - const Offset(110, 0));
    await b.moveTo(focal + const Offset(110, 0));
    await tester.pump();
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    expect(ex.columns(MediaKind.images), 2);
    final still = tester.getRect(find.byKey(ValueKey(before)).first);
    expect(still, after, reason: 'the view moved past the last step');
  });

  testWidgets('video of 18 November: what is over the photos fades while a pinch runs, then comes back', (tester) async {
    final library = _Library(photos: 90);
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    final grid = find.byKey(const ValueKey('media-images')).first;
    final center = tester.getCenter(grid);
    // The fade nearest to each box: the decor's, not a page's.
    double boxes() => find.descendant(of: grid, matching: find.byType(PickBox)).evaluate().map((e) {
      var opacity = 1.0;
      e.visitAncestorElements((a) {
        if (a.widget is! FadeTransition) return true;
        opacity = (a.widget as FadeTransition).opacity.value;
        return false;
      });
      return opacity;
    }).reduce(math.max);
    expect(boxes(), 1);
    final a = await tester.startGesture(center - const Offset(60, 0));
    final b = await tester.startGesture(center + const Offset(60, 0), pointer: 21);
    await tester.pump();
    await a.moveTo(center - const Offset(70, 0));
    await b.moveTo(center + const Offset(70, 0));
    await tester.pump(const Duration(milliseconds: 200));
    expect(boxes(), 0, reason: 'the boxes stay over the zooming grids');
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    expect(boxes(), 1);
  });

  testWidgets('real test of 22 November: a pinch chained to the last one, before it ended, still works', (tester) async {
    final library = _Library(photos: 90);
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    final ex = AppScope.of(tester.element(find.byType(Scaffold).first), listen: false).state.explorer;
    final center = tester.getCenter(find.byKey(const ValueKey('media-images')).first);
    Future<void> pinch(double from, double to, int pointer) async {
      final a = await tester.startGesture(center - Offset(from, 0), pointer: pointer);
      final b = await tester.startGesture(center + Offset(from, 0), pointer: pointer + 1);
      await tester.pump();
      for (var i = 1; i <= 12; i++) {
        final d = from + (to - from) * i / 12;
        await a.moveTo(center - Offset(d, 0));
        await b.moveTo(center + Offset(d, 0));
        await tester.pump(const Duration(milliseconds: 16));
      }
      await a.up();
      await b.up();
    }
    await pinch(60, 160, 30); // 3 → 2
    await tester.pump(const Duration(milliseconds: 60)); // still settling
    await pinch(160, 100, 40); // at once, back: 2 → 3
    await tester.pumpAndSettle();
    expect(ex.columns(MediaKind.images), 3);
    expect(find.byKey(const ValueKey('pinch-painting')), findsNothing, reason: 'the painting stayed over the grid');
  });

  testWidgets('real test of 22 November: a second finger while one scrolls is no pinch; the scroll goes on', (tester) async {
    final library = _Library(photos: 90);
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    final grid = find.byKey(const ValueKey('media-images')).first;
    final center = tester.getCenter(grid);
    final a = await tester.startGesture(center + const Offset(0, 100), pointer: 50);
    for (var i = 1; i <= 5; i++) {
      await a.moveTo(center + Offset(0, 100 - i * 20.0)); // scrolling
      await tester.pump(const Duration(milliseconds: 16));
    }
    final b = await tester.startGesture(center + const Offset(80, 0), pointer: 51); // the palm
    await tester.pump();
    await b.moveTo(center + const Offset(160, 0));
    await tester.pump();
    expect(find.byKey(const ValueKey('pinch-painting')), findsNothing);
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
  });

  testWidgets('WP-8.1b: an app shows its box once picked only', (tester) async {
    final library = _Library();
    await _app(tester, library);
    final notes = find.byKey(const ValueKey('app-org.notes'));
    expect(find.descendant(of: notes, matching: find.byType(PickBox)), findsNothing);
    await tester.tap(notes);
    await tester.pump();
    expect(find.descendant(of: notes, matching: find.byType(PickBox)), findsOneWidget);
  });

  testWidgets('real test of 22 November: a photo touched shows its box picked, and again unpicked', (tester) async {
    final (_, state) = await _app(tester, _Library());
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    bool on() => tester.widget<PickBox>(find.descendant(of: find.byKey(const ValueKey('media-2')), matching: find.byType(PickBox))).on;
    expect(on(), isFalse);
    await tester.tap(find.byKey(const ValueKey('media-2')));
    await tester.pump();
    expect(state.selection.count, 1);
    expect(on(), isTrue);
    await tester.tap(find.byKey(const ValueKey('media-2')));
    await tester.pump();
    expect(on(), isFalse);
  });

  testWidgets('22 November: the basket opens what is picked in every tab; one taken out, then all', (tester) async {
    final (_, state) = await _app(tester, _Library());
    await tester.tap(find.byKey(const ValueKey('app-org.notes')));
    await tester.pump();
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('media-1')));
    await tester.tap(find.byKey(const ValueKey('media-3')));
    await tester.pump();
    expect(state.selection.count, 3);

    await tester.tap(find.byKey(const Key('basket-open')));
    await tester.pumpAndSettle();
    expect(find.text('Ta sélection'), findsOneWidget);
    expect(find.byKey(const ValueKey('selection-/data/app/org.notes/base.apk')), findsOneWidget, reason: 'picked in another tab');
    expect(find.byKey(const ValueKey('selection-/storage/emulated/0/DCIM/Camera/IMG_1.jpg')), findsOneWidget);
    expect(find.text('Apps · 1'), findsOneWidget);
    expect(find.text('Photos · 2'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('selection-remove-/data/app/org.notes/base.apk')));
    await tester.pumpAndSettle();
    expect(state.selection.count, 2);
    expect(find.byKey(const ValueKey('selection-/data/app/org.notes/base.apk')), findsNothing);
    expect(find.byKey(const Key('selection-sheet-line')), findsOneWidget);

    await tester.tap(find.byKey(const Key('selection-clear')));
    await tester.pumpAndSettle();
    expect(state.selection.isEmpty, isTrue);
    expect(find.text('Ta sélection'), findsNothing, reason: 'nothing left: the sheet goes');
  });

  testWidgets('real test of 22 November: a long name shows in full, its row as tall as it needs', (tester) async {
    final (_, state) = await _app(tester, _Library(long: true));
    await tester.ensureVisible(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Documents'));
    await tester.pumpAndSettle();
    final short = tester.getSize(find.byKey(const ValueKey('file-rapport.pdf'))).height;
    final long = tester.getSize(find.byKey(const ValueKey('file-$_longName'))).height;
    expect(find.text(_longName), findsOneWidget);
    expect(long, greaterThan(short + 30), reason: 'on several lines, not cut');

    // Photos as a list: the same, and the rows below in their place.
    await tester.ensureVisible(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    state.explorer.setLayout(MediaKind.images, asList: true);
    await tester.pumpAndSettle();
    final rows = [for (final id in [1, 2, 3]) tester.getRect(find.byKey(ValueKey('media-$id')))];
    final longRow = rows.firstWhere((r) => r.height > rows.map((r) => r.height).reduce(math.min) + 30, orElse: () => Rect.zero);
    expect(longRow, isNot(Rect.zero), reason: 'the long name makes its row taller');
    expect(find.text(_longName), findsOneWidget);
    final sorted = [...rows]..sort((a, b) => a.top.compareTo(b.top));
    for (var i = 1; i < sorted.length; i++) {
      expect(sorted[i].top, greaterThanOrEqualTo(sorted[i - 1].bottom - 0.5), reason: 'no row over another');
    }
  });

  testWidgets('real test of 22 November: a photo taken while the app runs shows, without restarting it', (tester) async {
    final library = _Library();
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('media-4')), findsNothing);
    library.photos = 4;
    library.changed.add(null);
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('media-4')), findsOneWidget);
  });

  testWidgets('real test of 22 November: back in a folder goes up; at the top, it asks before quitting', (tester) async {
    final exits = _watchExits();
    final (_, state) = await _app(tester, _Library());
    await tester.ensureVisible(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('file-DCIM')));
    await tester.pumpAndSettle();
    expect(state.explorer.dir, '/storage/emulated/0/DCIM');

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(state.explorer.dir, '/storage/emulated/0', reason: 'up a folder, the app still there');
    expect(find.byKey(const Key('exit-dialog')), findsNothing);

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.text('Quitter medXfer ?'), findsOneWidget);
    expect(find.byKey(const Key('exit-keep')), findsNothing, reason: 'nothing runs: nothing to keep');
    await tester.tap(find.text('Annuler'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('exit-dialog')), findsNothing);
    expect(exits.calls, isEmpty);
  });

  testWidgets('22 November: connected, back says so; kept in the background, or all stopped', (tester) async {
    final exits = _watchExits();
    final (core, state) = await _app(tester, _Library());
    core.on('disconnect', (_) => const FakeAnswer());
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'PC'}));
    await tester.pumpAndSettle();

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('exit-running')), findsOneWidget);
    await tester.tap(find.byKey(const Key('exit-keep')));
    await tester.pumpAndSettle();
    expect(exits.calls, ['toBack']);
    expect(state.connection.session, isNotNull, reason: 'kept');

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('exit-quit')));
    await tester.pumpAndSettle();
    expect(core.calls.map((c) => c.action), contains('disconnect'));
    expect(exits.calls, ['toBack', 'quit']);
  });

  testWidgets('23 November: a large tile asks its sharp image once still on screen; a small one does not', (tester) async {
    final library = _Library();
    final (_, state) = await _app(tester, library);
    await tester.tap(find.text('Photos'));
    // The tiles, as soon as they are there.
    for (var i = 0; i < 60 && find.byKey(const ValueKey('media-3')).evaluate().isEmpty; i++) {
      await tester.pump(const Duration(milliseconds: 16));
    }
    await tester.pump();
    expect(library.keptAsked.toSet(), {1, 2, 3}, reason: 'one made already would come at once');
    expect(library.sharpAsked, isEmpty, reason: 'none made before the tile has stayed still');
    await tester.pumpAndSettle(const Duration(milliseconds: 100));
    await tester.pump(const Duration(milliseconds: 300));
    // 3 columns over 1080 px: a tile of about 359 px, more than Android's 270.
    expect(library.sharpAsked.map((a) => a.$1).toSet(), {1, 2, 3});
    expect(library.sharpAsked.map((a) => a.$2).toSet(), {864}, reason: 'one size, the tallest mosaic row (0.8 × 1080 px)');
    expect(library.prepared.last, (864, 1080, true), reason: 'made ahead, the app on screen and no transfer');

    library.sharpAsked.clear();
    state.explorer.setLayout(MediaKind.images, columns: 5);
    await tester.pumpAndSettle();
    await tester.pump(const Duration(milliseconds: 300));
    expect(library.sharpAsked, isEmpty, reason: "at 5 columns, Android's thumbnail is large enough");
  });

  testWidgets('real test of 23 November: fingers sliding while they pinch do not fling the list once lifted', (tester) async {
    final library = _Library(photos: 300);
    await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    final ex = AppScope.of(tester.element(find.byType(Scaffold).first), listen: false).state.explorer;
    final grid = find.byKey(const ValueKey('media-images'));
    final scroll = tester.state<ScrollableState>(find.descendant(of: grid, matching: find.byType(Scrollable)).first).position;
    scroll.jumpTo(600);
    await tester.pumpAndSettle();
    final center = tester.getCenter(grid);

    // Spread 3 → 2, both fingers going down fast meanwhile, lifted while moving.
    final a = await tester.startGesture(center - const Offset(40, 0), pointer: 11);
    final b = await tester.startGesture(center + const Offset(40, 0), pointer: 12);
    await tester.pump();
    for (var i = 1; i <= 10; i++) {
      final at = Duration(milliseconds: 16 * i);
      await a.moveTo(center + Offset(-40.0 - i * 8, i * 25.0), timeStamp: at);
      await b.moveTo(center + Offset(40.0 + i * 8, i * 25.0), timeStamp: at);
      await tester.pump(const Duration(milliseconds: 16));
    }
    await a.up(timeStamp: const Duration(milliseconds: 170));
    await b.up(timeStamp: const Duration(milliseconds: 170));
    await tester.pumpAndSettle();
    expect(ex.columns(MediaKind.images), 2);
    final settled = scroll.pixels;
    expect(settled, lessThan(scroll.maxScrollExtent), reason: 'flung to the end');
    await tester.pump(const Duration(seconds: 1));
    await tester.pumpAndSettle();
    expect(scroll.pixels, settled, reason: 'still moving on its own');
    // The photo under the fingers when they came down, still there (600 px into 3 columns).
    final stride3 = (392.7 - 2 * 0.75) / 3 + 0.75;
    final under = (((600 + (center.dy - tester.getTopLeft(grid).dy)) ~/ stride3) * 3 + 1);
    expect(find.byKey(ValueKey('media-${under + 1}')), findsOneWidget);
  });

  testWidgets('23 November: the mosaic shows each photo in its proportions, rows as wide as the screen', (tester) async {
    // A landscape photo, then three tall ones (9:16).
    final library = _Library(photos: 4, sizes: const [(1920, 1080), (1080, 1920), (1080, 1920), (1080, 1920)]);
    final (_, state) = await _app(tester, library);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    state.explorer.setLayout(MediaKind.images, columns: 2);
    await tester.pumpAndSettle();
    final grid = tester.getRect(find.byKey(const ValueKey('media-images')));
    Rect tile(int id) => tester.getRect(find.byKey(ValueKey('media-$id')));
    expect(tile(1).width, closeTo(grid.width, 0.5), reason: 'the landscape photo fills its row alone');
    expect(tile(1).width / tile(1).height, closeTo(16 / 9, 0.02));
    expect(tile(2).top, tile(3).top, reason: 'the tall ones share a row');
    expect(tile(2).height, tile(4).height);
    expect(tile(2).width / tile(2).height, closeTo(9 / 16, 0.02));
    expect(tile(4).right, closeTo(grid.right, 0.5));
  });

  testWidgets('WP-8.2: ⤢ opens a photo in full; « Sélectionner » there is the grid\'s selection; the next one by the arrow', (tester) async {
    final (_, state) = await _app(tester, _Library());
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-pages')), findsOneWidget);
    expect(find.text('IMG_1.jpg'), findsOneWidget);
    expect(state.selection.isEmpty, isTrue, reason: '⤢ opens, it does not pick');

    await tester.tap(find.byKey(const Key('media-select')));
    await tester.pump();
    expect(state.selection.contains('/storage/emulated/0/DCIM/Camera/IMG_1.jpg'), isTrue);
    expect(find.textContaining('Sélectionné'), findsOneWidget);

    await tester.tap(find.byKey(const Key('image-next')));
    await tester.pumpAndSettle();
    expect(find.text('IMG_2.jpg'), findsOneWidget);

    await tester.tap(find.byKey(const Key('player-close')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-pages')), findsNothing);
    bool on(int id) => tester.widget<PickBox>(find.descendant(of: find.byKey(ValueKey('media-$id')), matching: find.byType(PickBox))).on;
    expect(on(1), isTrue, reason: 'picked in the viewer, picked in the grid');
  });

  testWidgets('23 November: the strip under the photo goes to the one touched; « Infos » shows its details; « Partager »', (tester) async {
    final openWith = _FakeOpenWith();
    await _app(tester, _Library(photos: 6), openWith: openWith);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-strip')), findsOneWidget);
    expect(find.byKey(const Key('image-info')), findsNothing, reason: 'hidden until asked, as in the gallery');

    await tester.tap(find.byKey(const ValueKey('strip-3')));
    await tester.pumpAndSettle();
    expect(find.text('IMG_3.jpg'), findsOneWidget);

    await tester.tap(find.byKey(const Key('image-info-toggle')));
    await tester.pump();
    expect(find.byKey(const Key('image-info')), findsOneWidget);

    await tester.tap(find.byKey(const Key('image-share')));
    await tester.pump();
    expect(openWith.shared, [3]);
  });

  testWidgets('23 November: the photo flies from its tile; zoomed in by a double tap, the controls go, and come back', (tester) async {
    await _app(tester, _Library(photos: 3, sizes: const [(3000, 4000), (3000, 4000), (3000, 4000)]));
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    // The tile and the viewer share the photo's hero: it grows from one to the other.
    expect(find.byType(Hero), findsWidgets);
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(find.byKey(const Key('image-pages')), findsOneWidget);
    await tester.pumpAndSettle();

    double shown() => tester.widget<AnimatedOpacity>(find.ancestor(of: find.byKey(const Key('image-strip')), matching: find.byType(AnimatedOpacity)).first).opacity;
    expect(shown(), 1);
    final photo = find.byKey(const ValueKey('photo-1'));
    await tester.tap(photo);
    await tester.pump(const Duration(milliseconds: 60));
    await tester.tap(photo);
    await tester.pumpAndSettle();
    expect(shown(), 0, reason: 'zoomed in: the controls go');

    await tester.tap(photo);
    await tester.pump(const Duration(milliseconds: 60));
    await tester.tap(photo);
    await tester.pumpAndSettle();
    expect(shown(), 1, reason: 'the whole photo again: they come back');
  });

  testWidgets('23 November: two fingers spread on a photo zoom it in', (tester) async {
    await _app(tester, _Library(photos: 3, sizes: const [(3000, 4000), (3000, 4000), (3000, 4000)]));
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pumpAndSettle();
    final viewer = find.descendant(of: find.byKey(const ValueKey('photo-1')), matching: find.byType(InteractiveViewer));
    double scale() => tester.widget<InteractiveViewer>(viewer).transformationController!.value.getMaxScaleOnAxis();
    expect(scale(), 1);
    final center = tester.getCenter(find.byKey(const ValueKey('photo-1')));
    final a = await tester.startGesture(center - const Offset(30, 0), pointer: 21);
    final b = await tester.startGesture(center + const Offset(30, 0), pointer: 22);
    await tester.pump();
    for (var i = 1; i <= 10; i++) {
      await a.moveTo(center - Offset(30.0 + i * 12, 0));
      await b.moveTo(center + Offset(30.0 + i * 12, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    expect(scale(), greaterThan(1.5), reason: 'the pinch zoomed it in');
  });

  testWidgets("23 November: a hand's pinch zooms in at Android's touch slop, the pages held still", (tester) async {
    // On the phone a swipe starts after 8 dp, a pinch after 18: the pages took the pinch there,
    // never at Flutter's own slop in a test (real test of 23 November 2026).
    tester.view.gestureSettings = const ui.GestureSettings(physicalTouchSlop: 8 * 2.75);
    addTearDown(tester.view.resetGestureSettings);
    await _app(tester, _Library(photos: 3, sizes: const [(3000, 4000), (3000, 4000), (3000, 4000)]));
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-2')));
    await tester.pumpAndSettle();
    final pages = tester.widget<PageView>(find.byKey(const Key('image-pages'))).controller!;
    final viewer = find.descendant(of: find.byKey(const ValueKey('photo-2')), matching: find.byType(InteractiveViewer));
    double scale() => tester.widget<InteractiveViewer>(viewer).transformationController!.value.getMaxScaleOnAxis();
    expect(scale(), 1);
    final center = tester.getCenter(find.byKey(const ValueKey('photo-2')));
    // The thumb stays; the finger, set down after it and far from it, opens to the right.
    final thumb = await tester.startGesture(center + const Offset(-120, 80), pointer: 31);
    await tester.pump(const Duration(milliseconds: 20));
    final finger = await tester.startGesture(center + const Offset(60, -60), pointer: 32);
    await tester.pump(const Duration(milliseconds: 8));
    for (var i = 1; i <= 30; i++) {
      await finger.moveTo(center + Offset(60.0 + i * 3, -60.0 - i));
      await tester.pump(const Duration(milliseconds: 8));
    }
    await thumb.up();
    await finger.up();
    await tester.pumpAndSettle();
    expect(scale(), greaterThan(1.2), reason: 'the pinch zoomed it in');
    expect(pages.page, 1, reason: 'the pages did not take it');
  });

  testWidgets('23 November: the controls come once the photo has flown in, and go at once as it flies back', (tester) async {
    await _app(tester, _Library(photos: 3, sizes: const [(3000, 4000), (3000, 4000), (3000, 4000)]));
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 150));
    double shown() => tester.widget<AnimatedOpacity>(find.ancestor(of: find.byKey(const Key('image-strip')), matching: find.byType(AnimatedOpacity)).first).opacity;
    expect(shown(), 0, reason: 'the photo still flying, over them');
    // What flies is the viewer's photo, whole (its tile may crop it): both ways.
    expect(find.byKey(const Key('photo-flying')), findsOneWidget);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('photo-flying')), findsNothing);
    expect(shown(), 1, reason: 'landed: they come');
    await tester.tap(find.byKey(const Key('player-close')));
    await tester.pump();
    await tester.pump();
    expect(shown(), 0, reason: 'flying back over the grid: gone at once');
    await tester.pump(const Duration(milliseconds: 60));
    expect(find.byKey(const Key('photo-flying')), findsOneWidget);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-pages')), findsNothing);
  });

  testWidgets('23 November: swiped far, the viewer closes on a photo the grid brings into view, its tile under it', (tester) async {
    await _app(tester, _Library(photos: 90));
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    Finder tileOf(int id) => find.byWidgetPredicate((w) => w is Hero && w.tag == 'media-hero-grid-images-/storage/emulated/0/DCIM/Camera/IMG_$id.jpg');
    expect(tileOf(85), findsNothing, reason: 'far down the grid, not built');
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pumpAndSettle();
    tester.widget<PageView>(find.byKey(const Key('image-pages'))).controller!.jumpToPage(84);
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('player-close')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-pages')), findsNothing);
    expect(tileOf(85), findsOneWidget, reason: 'the grid brought it into view');
    final r = tester.getRect(tileOf(85));
    expect(r.top, greaterThanOrEqualTo(0));
    expect(r.bottom, lessThanOrEqualTo(tester.view.physicalSize.height / tester.view.devicePixelRatio));
  });

  testWidgets('23 November: opening, the black comes over the first 80 ms, and the photo is on its way from the first frame', (tester) async {
    await _app(tester, _Library(photos: 3, sizes: const [(3000, 4000), (3000, 4000), (3000, 4000)]));
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    final tile = tester.getRect(find.byKey(const ValueKey('media-1')));
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pump(); // pushed: the photo flies from the next frame
    await tester.pump(const Duration(milliseconds: 16));
    double black() => tester.widget<ColoredBox>(find.byKey(const Key('photo-backdrop'))).color.a;
    expect(black(), inExclusiveRange(0.05, 0.9), reason: 'coming, the grid still seen: no black flash');
    final flying = tester.getRect(find.byKey(const Key('photo-flying')));
    final screen = tester.view.physicalSize.width / tester.view.devicePixelRatio;
    expect((flying.width - tile.width) / (screen - tile.width), greaterThan(0.08), reason: "grown already (Flutter's own curve: 1 %)");
    await tester.pump(const Duration(milliseconds: 80));
    expect(black(), 1);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('photo-flying')), findsNothing);
  });

  testWidgets("23 November: Android's back drawn as the pages move: they come back to the photo shown and hold still", (tester) async {
    await _app(tester, _Library(photos: 3, sizes: const [(3000, 4000), (3000, 4000), (3000, 4000)]));
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pumpAndSettle();
    final pages = tester.widget<PageView>(find.byKey(const Key('image-pages'))).controller!;
    final center = tester.getCenter(find.byKey(const Key('image-pages')));
    // The finger drawing the back gesture swipes the pages too, a quarter of the way.
    final finger = await tester.startGesture(center);
    for (var i = 1; i <= 8; i++) {
      await finger.moveTo(center - Offset(i * 16.0, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    expect(pages.page, greaterThan(0.2));
    await tester.binding.handlePopRoute(); // taken as back
    expect(pages.page, 0, reason: 'back on the photo shown, the one flying back');
    await tester.pump();
    await finger.moveTo(center - const Offset(220, 0));
    await tester.pump(const Duration(milliseconds: 16));
    expect(pages.page, 0, reason: 'the pages hold still while it flies back: no other photo over the grid');
    await finger.up();
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-pages')), findsNothing);
  });

  testWidgets("23 November: a finger set down at a side, where Android's back gesture starts, does not swipe the photos", (tester) async {
    await _app(tester, _Library(photos: 3, sizes: const [(3000, 4000), (3000, 4000), (3000, 4000)]));
    tester.view.systemGestureInsets = const FakeViewPadding(left: 30 * 2.75, right: 30 * 2.75);
    addTearDown(tester.view.resetSystemGestureInsets);
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-1')));
    await tester.pumpAndSettle();
    final pages = tester.widget<PageView>(find.byKey(const Key('image-pages'))).controller!;
    final width = tester.view.physicalSize.width / tester.view.devicePixelRatio;
    final y = tester.getCenter(find.byKey(const Key('image-pages'))).dy;
    Future<void> swipeFrom(double x) async {
      final finger = await tester.startGesture(Offset(x, y));
      await tester.pump();
      for (var i = 1; i <= 10; i++) {
        await finger.moveTo(Offset(x - i * 24, y));
        await tester.pump(const Duration(milliseconds: 16));
      }
      await finger.up();
      await tester.pumpAndSettle();
    }

    await swipeFrom(width - 10);
    expect(pages.page, 0, reason: "from the side: Android's back, not the next photo");
    await swipeFrom(width - 60);
    expect(pages.page, 1, reason: 'from within: the next one');
  });

  testWidgets('23 November: flung fast, the tiles passing decode their thumbnails at once, not once it slows', (tester) async {
    final (_, state) = await _app(tester, _Library(photos: 300, thumbs: true));
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    final grid = find.byKey(const ValueKey('media-images'));
    final px = gridDecodePx(tester.element(grid));
    Iterable<int> tiles() => find
        .byWidgetPredicate((w) => w is GestureDetector && w.key is ValueKey<String> && RegExp(r'^media-\d+$').hasMatch((w.key! as ValueKey<String>).value))
        .evaluate()
        .map((e) => int.parse(((e.widget.key! as ValueKey<String>).value).substring(6)));
    final before = tiles().toSet();
    await tester.fling(grid, const Offset(0, -500), 12000);
    var seen = <int, Uint8List>{}; // tiles come with the fling, with their bytes at the frame before
    var checked = 0;
    for (var f = 0; f < 15; f++) {
      await tester.pump(const Duration(milliseconds: 16));
      final ids = tiles().where((id) => !before.contains(id)).toList();
      if (ids.isEmpty) continue;
      if (!Scrollable.recommendDeferredLoadingForContext(tester.element(find.byKey(ValueKey('media-${ids.first}'))))) break;
      for (final id in ids) {
        final bytes = seen[id];
        if (bytes == null) continue;
        checked++;
        final key = ResizeImage(MemoryImage(bytes), width: px);
        final cache = PaintingBinding.instance.imageCache;
        final decoded = cache.containsKey(key) || cache.statusForKey(await key.obtainKey(ImageConfiguration.empty)).live;
        expect(decoded, isTrue, reason: 'photo $id: decoding, or decoded');
      }
      seen = {};
      for (final id in ids) {
        final bytes = state.explorer.cached('images:$id');
        if (bytes != null) seen[id] = bytes;
      }
    }
    expect(checked, greaterThan(10), reason: 'tiles seen while Flutter would have put their decoding off');
  });

  testWidgets('WP-8.2: a video plays from ⤢; pause, the next one; a format not read says so, with another app', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    await tester.ensureVisible(find.text('Vidéos'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Vidéos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-101')));
    await tester.pumpAndSettle();
    final p = players.made.single;
    expect(p.opened, [101]);
    expect(p.playing, isTrue, reason: 'it plays once open');
    await tester.tap(find.byKey(const Key('player-play')));
    await tester.pump();
    expect(p.playing, isFalse);
    await tester.tap(find.byKey(const Key('player-next')));
    await tester.pumpAndSettle();
    expect(p.opened, [101, 102]);
    expect(find.text('film-2.mp4'), findsOneWidget);
  });

  testWidgets('WP-8.2: a video the phone does not read: said, and « Ouvrir avec une autre application »', (tester) async {
    final openWith = _FakeOpenWith();
    await _app(tester, _Library(videos: 1), players: _FakePlayers(fail: 'unsupported codec'), openWith: openWith);
    await tester.ensureVisible(find.text('Vidéos'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Vidéos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-101')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('cannot-play')), findsOneWidget);
    expect(find.text('Ce téléphone ne lit pas ce format'), findsOneWidget);
    await tester.tap(find.text('Ouvrir avec une autre application'));
    await tester.pump();
    expect(openWith.opened, [101]);
  });

  testWidgets('23 November: playing, the commands go after 3 s; a touch on the picture brings them back', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    expect(find.byKey(const Key('player-play')), findsOneWidget);
    await tester.pump(const Duration(seconds: 3));
    await tester.pump();
    expect(find.byKey(const Key('player-play')), findsNothing, reason: 'hidden while it plays');
    await tester.tap(find.byKey(const Key('video-surface')));
    await tester.pump(const Duration(milliseconds: 300)); // a single touch: no second one came
    expect(find.byKey(const Key('player-play')), findsOneWidget);
    expect(p.playing, isTrue);
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('23 November: two touches at the right, +10 s; one more, 10 s again; at the left, back', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    await tester.pump(const Duration(seconds: 4)); // the commands gone: the picture alone
    final r = tester.getRect(find.byKey(const Key('video-surface')));
    Future<void> touch(Offset at) async {
      await tester.tapAt(at);
      await tester.pump(const Duration(milliseconds: 100));
    }

    final right = Offset(r.left + r.width * 0.8, r.center.dy), left = Offset(r.left + r.width * 0.2, r.center.dy);
    await touch(right);
    await touch(right);
    expect(p.position, const Duration(seconds: 10));
    expect(find.text('+10 s'), findsOneWidget);
    await touch(right);
    expect(p.position, const Duration(seconds: 20), reason: 'each touch of the run, 10 s more');
    expect(find.text('+20 s'), findsOneWidget);
    await tester.pump(const Duration(seconds: 1)); // the run over
    expect(find.byKey(const Key('video-skip')), findsNothing);
    await touch(left);
    await touch(left);
    expect(p.position, const Duration(seconds: 10));
    expect(find.text('−10 s'), findsOneWidget);
    expect(find.byKey(const Key('player-play')), findsNothing, reason: 'the touches that seek do not bring the commands');
    await tester.pump(const Duration(seconds: 1));
  });

  testWidgets('24 November: two touches in the middle pause, and play again', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    await tester.pump(const Duration(seconds: 4));
    final center = tester.getCenter(find.byKey(const Key('video-surface')));
    Future<void> twice() async {
      await tester.tapAt(center);
      await tester.pump(const Duration(milliseconds: 100));
      await tester.tapAt(center);
      await tester.pump(const Duration(milliseconds: 100));
    }

    await twice();
    expect(p.playing, isFalse);
    expect(p.position, Duration.zero, reason: 'no seeking in the middle');
    await tester.pump(const Duration(seconds: 1));
    await twice();
    expect(p.playing, isTrue);
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets("24 November: « Format de l'image »: whole, filled, stretched, 16:9, 4:3; under the notch", (tester) async {
    final players = _FakePlayers();
    final (_, state) = await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    await tester.tap(find.byKey(const Key('player-menu')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('video-menu-picture')));
    await tester.pump();
    final fits = {'crop': BoxFit.cover, 'stretch': BoxFit.fill, 'wide': BoxFit.fill, 'tv': BoxFit.fill, 'fit': BoxFit.contain};
    for (final e in fits.entries) {
      await tester.tap(find.byKey(Key('video-picture-${e.key}')));
      await tester.pump();
      expect(p.fit, e.value, reason: e.key);
    }
    await tester.tap(find.byKey(const Key('video-picture-tv')));
    await tester.pump();
    expect(find.byWidgetPredicate((w) => w is AspectRatio && w.aspectRatio == 4 / 3), findsOneWidget);
    expect(state.settings.videoUnderNotch, isTrue);
    await tester.tap(find.byKey(const Key('video-under-notch')));
    await tester.pump();
    expect(state.settings.videoUnderNotch, isFalse);
    await tester.binding.handlePopRoute();
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('24 November: where it is, remembered as it plays, not only as it closes', (tester) async {
    final players = _FakePlayers();
    final (_, state) = await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    await p.seek(const Duration(seconds: 25));
    await tester.pump(const Duration(seconds: 6));
    expect(await state.resume.of(state.explorer.media(MediaKind.video).items.first), const Duration(seconds: 25));
  });

  testWidgets("24 November: unreadable, the engine's reason under the message", (tester) async {
    await _app(tester, _Library(videos: 1), players: _FakePlayers(fail: 'Failed to recognize file format.'));
    await tester.ensureVisible(find.text('Vidéos'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Vidéos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-101')));
    await tester.pumpAndSettle();
    expect(find.text('Raison : Failed to recognize file format.'), findsOneWidget);
  });

  testWidgets('24 November: a finger kept on the bar past 3 s: the commands stay, and the large time goes once it lifts', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    final bar = tester.getRect(find.byKey(const Key('player-seek')));
    final finger = await tester.startGesture(Offset(bar.left + 30, bar.center.dy));
    for (var i = 1; i <= 6; i++) {
      await finger.moveBy(const Offset(30, 0));
      await tester.pump(const Duration(milliseconds: 800));
    }
    expect(find.byKey(const Key('player-seek')), findsOneWidget, reason: 'not hidden under the finger');
    expect(find.byKey(const Key('video-seek-label')), findsOneWidget);
    await finger.up();
    await tester.pump(const Duration(seconds: 1));
    expect(find.byKey(const Key('video-seek-label')), findsNothing);
    expect(p.position, greaterThan(Duration.zero));
    await tester.pump(const Duration(seconds: 4));
  });

  test('24 November: the size a picture takes, laid out rather than scaled', () {
    const video = Size(1920, 1080), screen = Size(800, 400);
    expect(shownSize(BoxFit.contain, video, screen), const Size(711.1111111111111, 400));
    expect(shownSize(BoxFit.cover, video, screen).width, 800);
    expect(shownSize(BoxFit.fill, video, screen), screen);
  });

  testWidgets('24 November: upright, the bar takes the width, the volume bar 240 at most, the name on two lines', (tester) async {
    final players = _FakePlayers();
    final screen = _FakeScreen();
    await _app(tester, _Library(videos: 3), players: players, screen: screen);
    await _openVideo(tester, players);
    final width = tester.view.physicalSize.width / tester.view.devicePixelRatio;
    expect(tester.getSize(find.byKey(const Key('player-seek'))).width, greaterThan(width * 0.6));
    final name = tester.widget<Text>(find.text('film-1.mp4'));
    expect(name.maxLines, 2);
    await tester.pump(const Duration(seconds: 4));
    final r = tester.getRect(find.byKey(const Key('video-surface')));
    final finger = await tester.startGesture(Offset(r.left + r.width * 0.75, r.center.dy));
    for (var i = 1; i <= 6; i++) {
      await finger.moveBy(const Offset(0, -20));
      await tester.pump(const Duration(milliseconds: 16));
    }
    expect(tester.getSize(find.byKey(const Key('video-level'))).height, lessThanOrEqualTo(240));
    await finger.up();
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('24 November: closing, the picture goes at once, before the page fades out', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    expect(find.byKey(const ValueKey('layer-picture')), findsOneWidget);
    await tester.binding.handlePopRoute();
    await tester.pump();
    expect(find.byKey(const ValueKey('layer-picture')), findsNothing);
    expect(p.playing, isFalse);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('video-surface')), findsNothing);
  });

  testWidgets('24 November: in Files, ⤢ opens a photo among the photos of its folder, the queue made from them', (tester) async {
    final library = _Library();
    await _app(tester, library);
    await tester.ensureVisible(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('file-DCIM')));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('play-notes.txt')), findsNothing, reason: 'a text is no media');
    expect(find.byKey(const ValueKey('play-clip.mp4')), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('play-other.jpg')));
    await tester.pumpAndSettle();
    expect(library.mediaAsked.single, ['/storage/emulated/0/DCIM/known.jpg', '/storage/emulated/0/DCIM/other.jpg'], reason: 'the photos of the folder, in its order');
    expect(find.byKey(const Key('image-pages')), findsOneWidget);
    expect(find.text('2 / 2'), findsOneWidget, reason: 'the second photo of two: the video and the text left out');
    await tester.binding.handlePopRoute();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 60));
    expect(find.byKey(const Key('photo-flying')), findsOneWidget, reason: 'back to its row, flying');
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-pages')), findsNothing);
  });

  testWidgets('24 November: a photo in the Photos tab and in Files flies from the Files row, not from the tab not shown', (tester) async {
    await _app(tester, _Library());
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('media-1')), findsOneWidget, reason: 'the Photos tab, kept alive behind');
    await tester.ensureVisible(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('file-Camera')));
    await tester.pumpAndSettle();
    final row = tester.getRect(find.byKey(const ValueKey('play-IMG_1.jpg')));
    await tester.tap(find.byKey(const ValueKey('play-IMG_1.jpg')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 16));
    // Two heroes of one tag in a route: Flutter stops on it (a debug check), else picks one.
    expect(tester.takeException(), isNull);
    final flying = tester.getRect(find.byKey(const Key('photo-flying')));
    expect((flying.center.dy - row.center.dy).abs(), lessThan(200), reason: 'from its row');
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-pages')), findsOneWidget);
    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('24 November: a HEIC photo is decoded by the library, away from the thread that draws', (tester) async {
    // Decoded ahead as the neighbour of the test before: kept in the cache the tests share.
    PaintingBinding.instance.imageCache
      ..clear()
      ..clearLiveImages();
    final library = _Library();
    await _app(tester, library);
    await tester.ensureVisible(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('file-Camera')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('play-shot.heic')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('image-pages')), findsOneWidget);
    expect(library.decodedPhotos, contains('/storage/emulated/0/DCIM/Camera/shot.heic'));
    expect(library.decodedPhotos.where((p) => p.endsWith('.jpg')), isEmpty, reason: 'a JPEG, Flutter decodes it itself');
    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
  });

  testWidgets('24 November: ⤢ on a PDF opens the reader; a page by its number; selected as in the grid; closed, the document too', (tester) async {
    final pdf = _FakePdf();
    final (_, state) = await _app(tester, _Library(), pdf: pdf);
    final doc = await _openPdf(tester, pdf);
    expect(find.byKey(const Key('pdf-pages')), findsOneWidget);
    expect(find.textContaining('Page 1 sur 24'), findsOneWidget);
    expect(doc.asked.map((a) => a.$1), contains(0), reason: 'the first page drawn');
    expect(find.byKey(const Key('pdf-slider')), findsOneWidget);

    await tester.tap(find.byKey(const Key('pdf-goto')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('pdf-goto-field')), '12');
    await tester.tap(find.byKey(const Key('pdf-goto-go')));
    await tester.pumpAndSettle();
    expect(find.textContaining('Page 12 sur 24'), findsOneWidget);
    expect(doc.asked.map((a) => a.$1), contains(11));

    await tester.tap(find.byKey(const Key('media-select')));
    await tester.pump();
    expect(state.selection.contains('/storage/emulated/0/Documents/rapport.pdf'), isTrue);

    await tester.tap(find.byKey(const Key('player-close')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('pdf-pages')), findsNothing);
    expect(doc.closed, isTrue);
  });

  testWidgets('24 November: all the pages as thumbnails; touching one goes to it', (tester) async {
    final pdf = _FakePdf();
    await _app(tester, _Library(), pdf: pdf);
    await _openPdf(tester, pdf);
    await tester.tap(find.byKey(const Key('pdf-all')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('pdf-all-pages')), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('pdf-thumb-4')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('pdf-all-pages')), findsNothing);
    expect(find.textContaining('Page 5 sur 24'), findsOneWidget);
  });

  testWidgets('24 November: a double tap zooms in, the part shown drawn again sharp; a touch hides the bars, the page number stays', (tester) async {
    final pdf = _FakePdf();
    await _app(tester, _Library(), pdf: pdf);
    final doc = await _openPdf(tester, pdf);
    final screen = tester.view.physicalSize.width / tester.view.devicePixelRatio;
    expect(tester.getSize(find.byKey(const ValueKey('pdf-page-0'))).width, lessThan(screen));
    final at = tester.getCenter(find.byKey(const Key('pdf-pages')));
    await tester.tapAt(at);
    await tester.pump(const Duration(milliseconds: 60));
    await tester.tapAt(at);
    await tester.pumpAndSettle();
    expect(tester.getSize(find.byKey(const ValueKey('pdf-page-0'))).width, greaterThan(screen * 2));
    await tester.pump(const Duration(milliseconds: 200)); // drawn again once still
    await tester.pumpAndSettle();
    expect(doc.asked.where((a) => a.$3 != null), isNotEmpty, reason: 'the part shown, drawn at the zoom');

    await tester.tapAt(at);
    await tester.pump(const Duration(milliseconds: 400)); // a touch, once no second one came
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('pdf-header-line')), findsNothing);
    expect(find.byKey(const Key('pdf-page-chip')), findsOneWidget);
    expect(find.textContaining('250 %'), findsOneWidget);
  });

  testWidgets('24 November: two fingers pinch the pages larger; the list holds still meanwhile', (tester) async {
    final pdf = _FakePdf();
    await _app(tester, _Library(), pdf: pdf);
    await _openPdf(tester, pdf);
    final screen = tester.view.physicalSize.width / tester.view.devicePixelRatio;
    final c = tester.getCenter(find.byKey(const Key('pdf-pages')));
    final a = await tester.startGesture(c - const Offset(40, 0));
    final b = await tester.startGesture(c + const Offset(40, 0));
    await tester.pump();
    for (var i = 1; i <= 8; i++) {
      await a.moveTo(c - Offset(40.0 + i * 12, 0));
      await b.moveTo(c + Offset(40.0 + i * 12, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    final w = tester.getSize(find.byKey(const ValueKey('pdf-page-0'))).width;
    // The fingers 80 px apart, then 272: 3.4 times larger.
    expect(w, greaterThan(screen * 2.8));
    expect(w, lessThan(screen * 3.8));
  });

  testWidgets('real test of 24 November: zoomed on page 5, the reader stays on page 5 (it went back to the top of the first page)', (tester) async {
    final pdf = _FakePdf();
    await _app(tester, _Library(), pdf: pdf);
    await _openPdf(tester, pdf);
    await tester.tap(find.byKey(const Key('pdf-goto')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('pdf-goto-field')), '5');
    await tester.tap(find.byKey(const Key('pdf-goto-go')));
    await tester.pumpAndSettle();
    expect(find.textContaining('Page 5 sur 24'), findsOneWidget);
    final screen = tester.view.physicalSize.width / tester.view.devicePixelRatio;
    final c = tester.getCenter(find.byKey(const Key('pdf-pages')));

    // Two fingers.
    final a = await tester.startGesture(c - const Offset(40, 0));
    final b = await tester.startGesture(c + const Offset(40, 0));
    await tester.pump();
    for (var i = 1; i <= 6; i++) {
      await a.moveTo(c - Offset(40.0 + i * 8, 0));
      await b.moveTo(c + Offset(40.0 + i * 8, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    // While pinching too: still page 5 under the fingers.
    expect(find.byKey(const ValueKey('pdf-page-4')), findsOneWidget);
    expect(find.byKey(const ValueKey('pdf-page-0')), findsNothing);
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    expect(find.textContaining('Page 5 sur 24'), findsOneWidget, reason: 'still on page 5');
    expect(find.byKey(const ValueKey('pdf-page-0')), findsNothing);
    // Its middle under where the fingers were, not its corner.
    final page = tester.getRect(find.byKey(const ValueKey('pdf-page-4')));
    expect(page.width, greaterThan(screen * 1.8));
    expect(page.left, lessThan(0));
    expect(page.right, greaterThan(screen));

    // A double tap: back to the screen's width, still on page 5.
    await tester.tapAt(c);
    await tester.pump(const Duration(milliseconds: 60));
    await tester.tapAt(c);
    await tester.pumpAndSettle();
    expect(tester.getSize(find.byKey(const ValueKey('pdf-page-4'))).width, lessThan(screen));
    expect(find.textContaining('Page 5 sur 24'), findsOneWidget);
  });

  testWidgets('real test of 24 November: zoomed in, then out at once, the pages around show (a piece of page stayed alone in the dark)', (tester) async {
    final pdf = _FakePdf();
    await _app(tester, _Library(), pdf: pdf);
    await _openPdf(tester, pdf);
    final screen = tester.view.physicalSize.width / tester.view.devicePixelRatio;
    final c = tester.getCenter(find.byKey(const Key('pdf-pages')));
    // The part of the screen the pages are drawn in: the whole screen, whatever the zoom.
    Rect shown() => tester.getRect(find.descendant(of: find.byKey(const ValueKey('layer-pages')), matching: find.byType(SingleChildScrollView)));

    await tester.tapAt(c);
    await tester.pump(const Duration(milliseconds: 60));
    await tester.tapAt(c);
    await tester.pumpAndSettle();
    expect(tester.getSize(find.byKey(const ValueKey('pdf-page-0'))).width, greaterThan(screen * 2));

    // Two fingers 200 apart, then 100: half the zoom.
    final a = await tester.startGesture(c - const Offset(100, 0));
    final b = await tester.startGesture(c + const Offset(100, 0));
    await tester.pump();
    for (var i = 1; i <= 5; i++) {
      await a.moveTo(c - Offset(100.0 - i * 10, 0));
      await b.moveTo(c + Offset(100.0 - i * 10, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    expect(shown().width, greaterThanOrEqualTo(screen - 1), reason: 'not the old screen shrunk');
    final w = tester.getSize(find.byKey(const ValueKey('pdf-page-0'))).width;
    expect(w, closeTo((screen - 16) * 1.25, 30), reason: 'laid out at 125 %, while the fingers move');
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    expect(shown().width, greaterThanOrEqualTo(screen - 1));

    // A double tap back to the screen's width: laid out all along, not shrunk.
    await tester.tapAt(c);
    await tester.pump(const Duration(milliseconds: 60));
    await tester.tapAt(c);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(shown().width, greaterThanOrEqualTo(screen - 1), reason: 'halfway back');
    await tester.pumpAndSettle();
    expect(tester.getSize(find.byKey(const ValueKey('pdf-page-0'))).width, lessThan(screen));
  });

  testWidgets('24 November: a PDF locked by a password says so, and goes to another app', (tester) async {
    final pdf = _FakePdf(locked: true);
    final openWith = _FakeOpenWith();
    await _app(tester, _Library(), pdf: pdf, openWith: openWith);
    await _openPdf(tester, pdf);
    expect(find.byKey(const Key('pdf-protected')), findsOneWidget);
    expect(find.text('Ce PDF est protégé par un mot de passe'), findsOneWidget);
    await tester.tap(find.byKey(const Key('open-with')));
    await tester.pump();
    expect(openWith.files, ['open /storage/emulated/0/Documents/rapport.pdf application/pdf']);
  });

  testWidgets('24 November: without a PDF reader (the desktops for now), a PDF has no ⤢', (tester) async {
    await _app(tester, _Library());
    await tester.ensureVisible(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Fichiers'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Documents'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('play-rapport.pdf')), findsNothing);
  });

  test('24 November: the layouts and orders, kept and given back at the next start', () async {
    final store = MemorySettingsStore();
    final settings = AppSettings(store: store);
    final explorer = Explorer(_Library());
    explorer.onPrefs = () => settings.explorerPrefs = explorer.prefs;
    explorer.setLayout(MediaKind.images, columns: 5);
    explorer.setLayout(MediaKind.video, asList: true);
    explorer.setOrder(MediaKind.audio, const MediaOrder(sort: MediaSort.name, descending: false));
    await settings.saved;
    final again = AppSettings(store: store);
    await again.load();
    final next = Explorer(_Library())..applyPrefs(again.explorerPrefs);
    expect(next.columns(MediaKind.images), 5);
    expect(next.asList(MediaKind.video), isTrue);
    expect(next.order(MediaKind.audio).sort, MediaSort.name);
    expect(next.order(MediaKind.audio).descending, isFalse);
    expect(next.order(MediaKind.images).search, '');
  });

  testWidgets('24 November: in the light theme too, the player takes the bright accent chosen', (tester) async {
    final players = _FakePlayers();
    final (_, state) = await _app(tester, _Library(videos: 3), players: players);
    state.settings.theme = ThemeChoice.light;
    state.settings.accent = accents[2];
    await tester.pumpAndSettle();
    await _openVideo(tester, players);
    final play = tester.widget<IconButton>(find.byKey(const Key('player-play')));
    expect(play.style!.backgroundColor!.resolve({}), accents[2].bright);
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('23 November: sliding along the picture moves in the video, its time shown large', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    await tester.pump(const Duration(seconds: 4));
    final r = tester.getRect(find.byKey(const Key('video-surface')));
    final finger = await tester.startGesture(r.center);
    for (var i = 1; i <= 10; i++) {
      await finger.moveBy(Offset(r.width / 20, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    expect(find.byKey(const Key('video-seek-label')), findsOneWidget);
    await tester.pump(const Duration(milliseconds: 200)); // a seek every 150 ms at most
    expect(p.position, greaterThan(const Duration(seconds: 10)), reason: 'the picture follows the finger (24 November)');
    await finger.up();
    await tester.pump();
    // Half the width, for a video of a minute (under two: the width is all of it).
    expect(p.position.inSeconds, inInclusiveRange(27, 31));
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('23 November: up and down, the volume at the right, the brightness at the left, given back at the end', (tester) async {
    final players = _FakePlayers();
    final screen = _FakeScreen();
    await _app(tester, _Library(videos: 3), players: players, screen: screen);
    await _openVideo(tester, players);
    await tester.pump(const Duration(seconds: 4));
    final r = tester.getRect(find.byKey(const Key('video-surface')));
    Future<void> slideUp(double x) async {
      final finger = await tester.startGesture(Offset(r.left + r.width * x, r.center.dy));
      for (var i = 1; i <= 8; i++) {
        await finger.moveBy(const Offset(0, -20));
        await tester.pump(const Duration(milliseconds: 16));
      }
      expect(find.byKey(const Key('video-level')), findsOneWidget);
      await finger.up();
      await tester.pump();
    }

    await slideUp(0.75);
    expect(screen.volumeSet.last, greaterThan(0.4), reason: 'louder than it was');
    expect(screen.brightnessSet, isEmpty);
    await slideUp(0.25);
    expect(screen.brightnessSet.last, greaterThan(0.5));
    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(screen.brightnessSet.last, isNull, reason: "the system's brightness again once the player closes");
  });

  testWidgets("23 November: nothing starts at the sides, where Android's back gesture begins", (tester) async {
    final players = _FakePlayers();
    final screen = _FakeScreen();
    await _app(tester, _Library(videos: 3), players: players, screen: screen);
    tester.view.systemGestureInsets = const FakeViewPadding(left: 30 * 2.75, right: 30 * 2.75);
    addTearDown(tester.view.resetSystemGestureInsets);
    final p = await _openVideo(tester, players);
    await tester.pump(const Duration(seconds: 4));
    final r = tester.getRect(find.byKey(const Key('video-surface')));
    for (final at in [Offset(r.right - 8, r.center.dy), Offset(r.left + 8, r.center.dy)]) {
      final finger = await tester.startGesture(at);
      for (var i = 1; i <= 8; i++) {
        await finger.moveBy(Offset(at.dx < r.center.dx ? 20 : -20, -10));
        await tester.pump(const Duration(milliseconds: 16));
      }
      await finger.up();
      await tester.pump();
    }
    expect(screen.volumeSet, isEmpty);
    expect(screen.brightnessSet, isEmpty);
    expect(p.position, Duration.zero);
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('23 November: held, ×2 until the finger lifts; pinched, the picture fills the screen', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    await tester.pump(const Duration(seconds: 4));
    final center = tester.getCenter(find.byKey(const Key('video-surface')));
    final finger = await tester.startGesture(center);
    await tester.pump(const Duration(milliseconds: 600));
    expect(p.speed, 2);
    expect(find.byKey(const Key('video-fast')), findsOneWidget);
    await finger.up();
    await tester.pump();
    expect(p.speed, 1);

    expect(p.fit, BoxFit.contain);
    final a = await tester.startGesture(center - const Offset(40, 0), pointer: 31);
    final b = await tester.startGesture(center + const Offset(40, 0), pointer: 32);
    for (var i = 1; i <= 8; i++) {
      await a.moveBy(const Offset(-12, 0));
      await b.moveBy(const Offset(12, 0));
      await tester.pump(const Duration(milliseconds: 16));
    }
    await a.up();
    await b.up();
    await tester.pump();
    expect(p.fit, BoxFit.cover);
    expect(find.text("Remplir l'écran"), findsOneWidget);
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('23 November: the menu ⋮ sets the speed; back closes it, the video stays', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    await tester.tap(find.byKey(const Key('player-menu')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('video-menu-speed')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('video-speed-1.5')));
    await tester.pump();
    expect(p.speed, 1.5);
    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('video-panel')), findsNothing);
    expect(find.byKey(const Key('video-surface')), findsOneWidget, reason: 'the player still open');
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('23 November: a file with subtitles and two sound tracks: CC shows them, the sound track is chosen', (tester) async {
    final players = _FakePlayers(tracks: true);
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    expect(find.byKey(const Key('video-subtitle')), findsNothing);
    await tester.tap(find.byKey(const Key('player-subtitles')));
    await tester.pump();
    expect(p.subtitleTrack, 's1');
    expect(find.text('Bonjour.'), findsOneWidget);
    await tester.tap(find.byKey(const Key('player-subtitles')));
    await tester.pump();
    expect(p.subtitleTrack, isNull);
    expect(find.byKey(const Key('video-subtitle')), findsNothing);
    await tester.tap(find.byKey(const Key('player-audio')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('video-track-a2')));
    await tester.pump();
    expect(p.audioTrack, 'a2');
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('23 November: a video goes on where it was left, « Recommencer » at hand', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    await p.seek(const Duration(seconds: 30));
    await tester.tap(find.byKey(const Key('player-next')));
    await tester.pumpAndSettle();
    expect(p.opened, [101, 102]);
    expect(find.byKey(const Key('video-resume')), findsNothing, reason: 'never seen: from its start');
    await tester.tap(find.byKey(const Key('player-previous')));
    await tester.pumpAndSettle();
    expect(p.opened, [101, 102, 101]);
    expect(p.position, const Duration(seconds: 30));
    expect(find.text('Reprise à 0:30'), findsOneWidget);
    await tester.tap(find.byKey(const Key('video-restart')));
    await tester.pump();
    expect(p.position, Duration.zero);
    await tester.pump(const Duration(seconds: 6));
  });

  testWidgets('23 November: at the end of a video, the next one; autoplay off, it stays', (tester) async {
    final players = _FakePlayers();
    final (_, state) = await _app(tester, _Library(videos: 3), players: players);
    final p = await _openVideo(tester, players);
    p.finish();
    await tester.pumpAndSettle();
    expect(p.opened, [101, 102]);
    await tester.tap(find.byKey(const Key('player-up-next')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('video-autoplay')));
    await tester.pump();
    expect(state.settings.videoAutoplay, isFalse);
    await tester.tap(find.byKey(const Key('video-panel-scrim')), warnIfMissed: false);
    await tester.pump();
    p.finish();
    await tester.pumpAndSettle();
    expect(p.opened, [101, 102]);
    expect(find.byKey(const Key('player-play')), findsOneWidget, reason: 'the commands, to go on by hand');
    await tester.pump(const Duration(seconds: 4));
  });

  testWidgets('23 November: turned sideways, one row: « Sélectionner » and the tracks, ⏮ ⏯ ⏭ in the middle, picture, turn, next', (tester) async {
    final players = _FakePlayers(tracks: true);
    await _app(tester, _Library(videos: 3), players: players);
    await _openVideo(tester, players);
    tester.view.physicalSize = const Size(2400, 1080);
    await tester.pump();
    for (final key in ['media-select', 'player-subtitles', 'player-audio', 'player-previous', 'player-play', 'player-next', 'player-picture', 'player-rotate', 'player-up-next']) {
      expect(find.byKey(Key(key)), findsOneWidget, reason: key);
    }
    final play = tester.getCenter(find.byKey(const Key('player-play')));
    expect(play.dx, closeTo(2400 / 2.75 / 2, 2), reason: 'in the middle');
    expect(tester.getCenter(find.byKey(const Key('player-rotate'))).dy, closeTo(play.dy, 2), reason: 'one row');
    await tester.tap(find.byKey(const Key('player-menu')));
    await tester.pump();
    expect(tester.getRect(find.byKey(const Key('video-panel'))).right, closeTo(2400 / 2.75, 1), reason: 'the menu at the side');
    expect(tester.takeException(), isNull);
    await tester.binding.handlePopRoute();
    await tester.pump(const Duration(seconds: 4));
  });

  test('23 November: two engines: ExoPlayer first; libmpv for an AVI, a failure, or a video without its picture', () async {
    MediaEntry video(String name) => MediaEntry(id: 1, kind: MediaKind.video, name: name, path: '/v/$name', size: 1);
    var systemFails = false, systemBlind = false;
    final made = <_FakePlayer>[];
    final player = TwoEngines(
      system: () => _FakePlayer(systemFails ? 'codec' : null, picture: !systemBlind, name: 'ExoPlayer')..also(made.add),
      wide: () => _FakePlayer(null, name: 'libmpv')..also(made.add),
    );
    await player.open(video('a.mp4'));
    expect(player.engine, 'ExoPlayer');
    await player.setSpeed(1.5);
    await player.open(video('b.AVI'));
    expect(player.engine, 'libmpv', reason: 'read by libmpv straight away');
    expect(made.first.disposed, isTrue, reason: 'one engine at a time');
    expect(player.speed, 1.5, reason: 'the speed kept from one file to the next');
    systemFails = true;
    await player.open(video('c.mp4'));
    expect(player.engine, 'libmpv', reason: 'ExoPlayer failed');
    expect(player.error, isNull);
    systemFails = false;
    systemBlind = true;
    await player.open(video('d.mp4'));
    expect(player.engine, 'libmpv', reason: 'ExoPlayer played it without its picture');
    systemBlind = false;
    await player.open(video('e.mp4'));
    expect(player.engine, 'ExoPlayer');
    // Failing as it starts to play (24 November): libmpv goes on from there.
    await made.last.seek(const Duration(seconds: 12));
    made.last.breakDown('decoder');
    await Future<void>.delayed(Duration.zero);
    await Future<void>.delayed(Duration.zero);
    expect(player.engine, 'libmpv');
    expect(player.error, isNull);
    expect(player.position, const Duration(seconds: 12));
    expect(player.playing, isTrue);
    player.dispose();
  });

  testWidgets('WP-8.2: ▶ plays a song; at its end, the next one', (tester) async {
    final players = _FakePlayers();
    await _app(tester, _Library(songs: 2), players: players);
    await tester.ensureVisible(find.text('Musique'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Musique'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('open-9')));
    await tester.pumpAndSettle();
    final p = players.made.single;
    expect(p.opened, [9]);
    expect(p.playing, isTrue);
    p.finish();
    await tester.pumpAndSettle();
    expect(p.opened, [9, 11]);
    expect(find.text('chanson-2.mp3'), findsWidgets);
  });

  test('the plan: descriptors without folders, paths with them, never apps with folders', () {
    final s = Selection()
      ..toggle(const Pick(path: '/a.jpg', name: 'a.jpg', size: 1, kind: PickKind.media))
      ..toggle(const Pick(path: '/x/base.apk', name: 'X.apk', size: 1, kind: PickKind.app));
    expect(s.plan(), isA<ByDescriptor>());
    s.toggle(const Pick(path: '/Docs', name: 'Docs', size: 0, kind: PickKind.folder));
    expect(s.plan(), isA<Mixed>());
    s.toggle(const Pick(path: '/x/base.apk', name: 'X.apk', size: 1, kind: PickKind.app));
    expect((s.plan() as ByPath).paths, ['/a.jpg', '/Docs']);
  });
}
