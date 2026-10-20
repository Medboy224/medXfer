// The explorer by categories (WP-B.9) on a fake library: tabs, picking across tabs, the
// basket, select all, and how the selection goes to the core.
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/library/selection.dart';
import 'package:medxfer/platform/file_picker.dart';
import 'package:medxfer/platform/folders.dart';
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
  bool allowed = true;
  final opened = <List<({String path, String name})>>[];

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
  Future<List<MediaEntry>> media(MediaKind kind, {int offset = 0, int limit = 200}) async => offset > 0
      ? const []
      : switch (kind) {
          MediaKind.images => [
            for (var i = 1; i <= 3; i++)
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
    expect(find.text('1 élément · 4 Mo'), findsOneWidget);

    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('media-2')));
    await tester.pump();
    expect(find.text('2 éléments · 5 Mo'), findsOneWidget);

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
    expect(find.text('3 éléments · 3 Mo'), findsOneWidget);
    await tester.tap(find.byKey(const Key('select-all')));
    await tester.pump();
    expect(state.selection.isEmpty, isTrue);
  });

  testWidgets('photos by folder', (tester) async {
    await _app(tester, _Library());
    await tester.tap(find.text('Photos'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('media-3')), findsOneWidget);
    await tester.tap(find.widgetWithText(ChoiceChip, 'Camera'));
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
