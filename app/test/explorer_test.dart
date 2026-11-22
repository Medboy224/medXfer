// The explorer by categories (WP-B.9) on a fake library: tabs, picking across tabs, the
// basket, select all, and how the selection goes to the core.
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/features/explorer/gallery_toolbar.dart';
import 'package:medxfer/library/selection.dart';
import 'package:medxfer/platform/file_picker.dart';
import 'package:medxfer/platform/folders.dart';
import 'package:medxfer/widgets/v4.dart';
import 'package:medxfer/platform/library.dart';
import 'package:medxfer/platform/storage.dart';
import 'package:medxfer/settings.dart';
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

/// A phone with two apps, three photos, a song, and a folder.
class _Library implements Library {
  _Library({this.photos = 3});

  /// How many photos the phone holds.
  final int photos;
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
              MediaEntry(id: i, kind: kind, name: 'IMG_$i.jpg', path: '/storage/emulated/0/DCIM/Camera/IMG_$i.jpg', size: 1 << 20, album: i < 3 ? 'Camera' : 'WhatsApp'),
          ],
          MediaKind.audio => [
            const MediaEntry(id: 9, kind: MediaKind.audio, name: 'chanson.mp3', path: '/storage/emulated/0/Music/chanson.mp3', size: 5 << 20, album: 'Album', durationMs: 200000),
          ],
          MediaKind.video => const [],
        };
  @override
  Future<Uint8List?> thumbnail(MediaKind kind, int id, int size) async => null;
  @override
  Future<bool> mediaAllowed() async => allowed;
  @override
  Future<bool> askMedia() async => allowed = true;
  @override
  String get storageRoot => '/storage/emulated/0';
  @override
  Future<List<FileEntry>> listDir(String path) async => const [];
  @override
  Future<Uint8List?> fileThumbnail(String path, int px) async => null;
  @override
  Future<int> dirSize(String path) async => 7 << 20;
  @override
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200}) async => offset > 0
      ? const []
      : switch (category) {
          FileCategory.documents => const [FileEntry(name: 'rapport.pdf', path: '/storage/emulated/0/Documents/rapport.pdf', dir: false, size: 2 << 20)],
          _ => const [],
        };
  @override
  Future<List<OpenFile>> open(List<({String path, String name})> files) async {
    opened.add(files);
    return [for (var i = 0; i < files.length; i++) OpenFile(fd: 100 + i, name: files[i].name)];
  }
}

Future<(FakeCore, AppState)> _app(WidgetTester tester, _Library library) async {
  final core = FakeCore()..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'Galaxy'})]));
  final state = AppState(core: core, folders: const _TestFolders(), library: library, storage: const _Storage(), history: MemoryHistoryStore());
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
    await a.up();
    await b.up();
    await tester.pumpAndSettle();
    expect(ex.columns(MediaKind.images), 2);
    expect(scroll.pixels, before, reason: 'the two fingers scrolled the list');

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
