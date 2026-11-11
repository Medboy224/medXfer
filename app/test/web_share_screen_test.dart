// « Partager avec un navigateur » (WP-W.5 of the J7 plan) on the fake core: what is offered,
// edited before and during the session, the QR code and PIN, the guest's upload accepted, and
// the screen left and found again with the session still running (real test of 10 November).
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/platform/file_picker.dart';
import 'package:medxfer/platform/folders.dart';
import 'package:medxfer/settings.dart';
import 'package:medxfer/transfer/history.dart';

class _Folders implements Folders {
  const _Folders();
  @override
  Future<String> dataDir() async => '/tmp/medxfer-test/data';
  @override
  Future<String> downloadDir() async => '/tmp/medxfer-test/out';
}

/// Android's picker: files open already, a new descriptor each time.
class _Picker implements FilePicker {
  int _fd = 40;
  final closed = <int>[];

  @override
  bool get supported => true;
  @override
  bool get folders => false;
  @override
  Future<Picked> files() async => Picked(files: [OpenFile(fd: ++_fd, name: 'Vacances-$_fd.mp4')]);
  @override
  Future<String?> folder() async => null;
  @override
  Future<void> close(List<OpenFile> files) async => closed.addAll([for (final f in files) f.fd]);
}

const _started = CoreEvent('web_share_started', data: {
  'url': 'https://192.168.1.20:19999/s#t=0123456789abcdef0123456789abcdef',
  'token': '0123456789abcdef0123456789abcdef',
  'pin': '054461',
  'fingerprint': '9E:81:F2:40',
  'addr': '192.168.1.20',
  'port': 19999,
  'receive': true,
  'expires': '2026-11-10T12:00:00Z',
  'items': [
    {'id': '0', 'name': 'Vacances-41.mp4', 'size': 2048},
  ],
});

void main() {
  testWidgets('J7: chosen, edited, started; more added and withdrawn; the upload accepted; the screen left and found again, the session running', (tester) async {
    final picker = _Picker();
    final core = FakeCore()
      ..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'Galaxy A57', 'instance': 'medXfer-7810'})]))
      ..on('web_share_start', (_) => const FakeAnswer(replies: [_started]))
      ..on('web_share_answer', (_) => const FakeAnswer())
      ..on('web_share_add', (_) => const FakeAnswer(replies: [
            CoreEvent('web_share_added', data: {
              'count': 2,
              'items': [
                {'id': '1', 'name': 'Vacances-43.mp4', 'size': 10},
              ],
            }),
          ]))
      ..on('web_share_remove', (p) => FakeAnswer(replies: [CoreEvent('web_share_removed', data: {'id': p['id']})]))
      ..on('web_share_stop', (_) => const FakeAnswer(broadcast: [CoreEvent('web_share_closed', data: {'reason': 'stopped'})]));
    final state = AppState(core: core, folders: const _Folders(), filePicker: picker, history: MemoryHistoryStore());
    tester.platformDispatcher.localesTestValue = [const Locale('fr')];
    addTearDown(tester.platformDispatcher.clearLocalesTestValue);
    tester.view.physicalSize = const Size(1080, 2400);
    tester.view.devicePixelRatio = 2.75;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(MedxferApp(state: state, settings: AppSettings()));
    await state.start();
    await tester.pumpAndSettle();

    Future<void> openScreen() async {
      await tester.tap(find.byKey(const Key('home-menu')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('menu-browser')));
      await tester.pumpAndSettle();
    }

    await openScreen();
    expect(find.text('Aucun : l’invité pourra seulement vous envoyer des fichiers.'), findsOneWidget);
    await tester.tap(find.byKey(const Key('web-share-files')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('web-share-files')));
    await tester.pumpAndSettle();
    expect(find.text('Vacances-41.mp4'), findsOneWidget);
    expect(find.text('Vacances-42.mp4'), findsOneWidget);
    // Withdrawn before the start: its file, open already, closed.
    await tester.tap(find.descendant(of: find.byKey(const ValueKey('web-share-draft-1')), matching: find.byIcon(Icons.close_rounded)));
    await tester.pumpAndSettle();
    expect(find.text('Vacances-42.mp4'), findsNothing);
    expect(picker.closed, [42]);
    // Left and found again before the start: as it was.
    await tester.tap(find.byType(BackButton));
    await tester.pumpAndSettle();
    await openScreen();
    expect(find.text('Vacances-41.mp4'), findsOneWidget);

    await tester.tap(find.byKey(const Key('web-share-start')));
    await tester.pumpAndSettle();
    expect(core.calls.firstWhere((c) => c.action == 'web_share_start').payload, {
      'items': [
        {'fd': 41, 'name': 'Vacances-41.mp4'},
      ],
      'receive': true,
      'auto_accept': false,
    });
    expect(find.byKey(const Key('web-share-qr')), findsOneWidget);
    expect(find.text('054 461'), findsOneWidget);
    expect(find.textContaining('https://192.168.1.20:19999/s'), findsOneWidget);
    expect(find.byKey(const Key('web-share-warning')), findsOneWidget);
    expect(find.byKey(const ValueKey('web-share-item-0')), findsOneWidget);

    // More offered, then one withdrawn, the session running.
    await tester.ensureVisible(find.byKey(const Key('web-share-files')));
    await tester.tap(find.byKey(const Key('web-share-files')));
    await tester.pumpAndSettle();
    expect(core.calls.last.payload, {
      'items': [
        {'fd': 43, 'name': 'Vacances-43.mp4'},
      ],
    });
    expect(find.byKey(const ValueKey('web-share-item-1')), findsOneWidget);
    await tester.tap(find.descendant(of: find.byKey(const ValueKey('web-share-item-0')), matching: find.byIcon(Icons.close_rounded)));
    await tester.pumpAndSettle();
    expect(core.calls.last.payload, {'id': '0'});
    expect(find.byKey(const ValueKey('web-share-item-0')), findsNothing);

    core.emit(const CoreEvent('web_share_upload_request', data: {
      'ticket': 't1',
      'total': 2048,
      'auto': false,
      'files': [
        {'name': 'photo.jpg', 'size': 2048},
      ],
    }));
    await tester.pumpAndSettle();
    expect(find.text('L’invité veut envoyer 1 fichier (2 Ko).'), findsOneWidget);
    await tester.ensureVisible(find.byKey(const Key('web-share-accept')));
    await tester.tap(find.byKey(const Key('web-share-accept')));
    await tester.pumpAndSettle();
    expect(core.calls.last.payload, {'ticket': 't1', 'accept': true});
    core
      ..emit(const CoreEvent('web_share_upload_accepted', data: {'ticket': 't1'}))
      ..emit(const CoreEvent('web_share_upload_file_done', data: {'ticket': 't1', 'name': 'photo.jpg'}))
      ..emit(const CoreEvent('web_share_upload_done', data: {'ticket': 't1'}));
    await tester.pumpAndSettle();
    expect(state.webShare.received, ['photo.jpg']);

    // Left: the session goes on, its banner on the home, a tap back to it.
    await tester.tap(find.byType(BackButton));
    await tester.pumpAndSettle();
    expect(core.calls.where((c) => c.action == 'web_share_stop'), isEmpty);
    expect(find.byKey(const Key('web-share-banner')), findsOneWidget);
    expect(find.text('code 054 461'), findsOneWidget);
    await tester.tap(find.byKey(const Key('web-share-banner')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('web-share-qr')), findsOneWidget);
    await tester.scrollUntilVisible(find.byKey(const Key('web-share-received')), 200, scrollable: find.byType(Scrollable).last);
    expect(find.byKey(const Key('web-share-received')), findsOneWidget);

    // Stopped by the host: the banner goes.
    await tester.scrollUntilVisible(find.byKey(const Key('web-share-stop')), 300, scrollable: find.byType(Scrollable).last);
    await tester.drag(find.byType(Scrollable).last, const Offset(0, -300));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('web-share-stop')));
    await tester.pumpAndSettle();
    expect(core.calls.where((c) => c.action == 'web_share_stop'), hasLength(1));
    expect(state.webShare.session, isNull);
    await tester.tap(find.byType(BackButton));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('web-share-banner')), findsNothing);
  });

  testWidgets('J7: the guest\'s network chosen, never mobile data; none, said (real tests of 10 November)', (tester) async {
    var networks = <Map<String, Object?>>[
      {'ip': '192.168.49.174', 'kind': 'wifi'},
      {'ip': '192.168.81.175', 'kind': 'ethernet'},
    ];
    final core = FakeCore()
      ..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'PC', 'instance': 'medXfer-7810'})]))
      ..on('web_share_networks', (_) => FakeAnswer(replies: [CoreEvent('web_share_networks', data: {'networks': networks})]))
      ..on('web_share_start', (p) => p['addr'] == null
          ? const FakeAnswer(ok: false, error: 'no_local_network', replies: [CoreEvent('action_error', data: {'error': 'no_local_network'})])
          : FakeAnswer(replies: [
              CoreEvent('web_share_started', data: {..._started.map, 'addr': p['addr'], 'kind': 'ethernet'}),
            ]))
      ..on('web_share_stop', (_) => const FakeAnswer(broadcast: [CoreEvent('web_share_closed', data: {'reason': 'stopped'})]));
    final state = AppState(core: core, folders: const _Folders(), filePicker: _Picker(), history: MemoryHistoryStore());
    tester.platformDispatcher.localesTestValue = [const Locale('fr')];
    addTearDown(tester.platformDispatcher.clearLocalesTestValue);
    tester.view.physicalSize = const Size(1080, 2400);
    tester.view.devicePixelRatio = 2.75;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(MedxferApp(state: state, settings: AppSettings()));
    await state.start();
    await tester.pumpAndSettle();

    Future<void> openScreen() async {
      await tester.tap(find.byKey(const Key('home-menu')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('menu-browser')));
      await tester.pumpAndSettle();
    }

    // Two networks: the Wi-Fi first, the cable chosen.
    await openScreen();
    expect(find.text('Wi-Fi · 192.168.49.174'), findsOneWidget);
    expect(find.text('Câble · 192.168.81.175'), findsOneWidget);
    expect(state.webShare.network, '192.168.49.174');
    await tester.ensureVisible(find.byKey(const ValueKey('web-share-network-192.168.81.175')));
    await tester.tap(find.byKey(const ValueKey('web-share-network-192.168.81.175')));
    await tester.pumpAndSettle();
    expect(find.text('L’invité doit être sur le même réseau filaire que cet appareil.'), findsOneWidget);
    await tester.ensureVisible(find.byKey(const Key('web-share-start')));
    await tester.tap(find.byKey(const Key('web-share-start')));
    await tester.pumpAndSettle();
    expect(core.calls.lastWhere((c) => c.action == 'web_share_start').payload['addr'], '192.168.81.175');
    expect(find.byKey(const Key('web-share-same-network')), findsOneWidget);

    await tester.scrollUntilVisible(find.byKey(const Key('web-share-stop')), 300, scrollable: find.byType(Scrollable).last);
    await tester.tap(find.byKey(const Key('web-share-stop')));
    await tester.pumpAndSettle();

    // Only mobile data: said, and the core's refusal said in words.
    networks = [];
    await tester.ensureVisible(find.byKey(const Key('web-share-networks-refresh')));
    await tester.tap(find.byKey(const Key('web-share-networks-refresh')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('web-share-no-network')), findsOneWidget);
    await tester.ensureVisible(find.byKey(const Key('web-share-start')));
    await tester.tap(find.byKey(const Key('web-share-start')));
    await tester.pumpAndSettle();
    expect(state.webShare.session, isNull);
    expect(state.webShare.error, 'no_local_network');
    expect(find.textContaining('les données mobiles ne servent jamais'), findsNWidgets(2));
  });
}