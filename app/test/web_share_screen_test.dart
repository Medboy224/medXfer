// « Partager avec un navigateur » (WP-W.5 of the J7 plan) on the fake core: what is offered,
// the session's QR code and PIN, the guest's upload accepted, and the session ended on leaving.
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

/// Android's picker: files open already.
class _Picker implements FilePicker {
  @override
  bool get supported => true;
  @override
  bool get folders => false;
  @override
  Future<Picked> files() async => const Picked(files: [OpenFile(fd: 41, name: 'Vacances.mp4')]);
  @override
  Future<String?> folder() async => null;
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
});

void main() {
  testWidgets('J7: a file offered, the session\'s code and PIN, the guest\'s upload accepted, the session ended on leaving', (tester) async {
    final core = FakeCore()
      ..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'Galaxy A57', 'instance': 'medXfer-7810'})]))
      ..on('web_share_start', (_) => const FakeAnswer(replies: [_started]))
      ..on('web_share_answer', (_) => const FakeAnswer())
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

    await tester.tap(find.byKey(const Key('home-menu')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('menu-browser')));
    await tester.pumpAndSettle();
    expect(find.text('Aucun : l’invité pourra seulement vous envoyer des fichiers.'), findsOneWidget);
    await tester.tap(find.byKey(const Key('web-share-files')));
    await tester.pumpAndSettle();
    expect(find.text('Vacances.mp4'), findsOneWidget);
    await tester.tap(find.byKey(const Key('web-share-start')));
    await tester.pumpAndSettle();
    expect(core.calls.firstWhere((c) => c.action == 'web_share_start').payload, {
      'items': [
        {'fd': 41, 'name': 'Vacances.mp4'},
      ],
      'receive': true,
      'auto_accept': false,
    });
    expect(find.byKey(const Key('web-share-qr')), findsOneWidget);
    expect(find.text('054 461'), findsOneWidget);
    expect(find.textContaining('https://192.168.1.20:19999/s'), findsOneWidget);
    expect(find.byKey(const Key('web-share-warning')), findsOneWidget);

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
    expect(find.byKey(const Key('web-share-received')), findsOneWidget);
    expect(find.byKey(const Key('web-share-accept')), findsNothing);

    // Leaving the screen ends the session (WEB-10).
    await tester.tap(find.byType(BackButton));
    await tester.pumpAndSettle();
    expect(core.calls.where((c) => c.action == 'web_share_stop'), hasLength(1));
    expect(state.webShare.session, isNull);
  });
}
