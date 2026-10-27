// The screens of WP-B.3 on the fake core: the incoming offer, the transfer screen with each
// file's controls, sending from a desktop, and the History tab.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/platform/file_picker.dart';
import 'package:medxfer/platform/folders.dart';
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

class _Picker implements FilePicker {
  const _Picker(this.paths);
  final List<String> paths;
  @override
  bool get supported => true;
  @override
  bool get folders => true;
  @override
  Future<Picked> files() async => Picked(paths: paths);
  @override
  Future<String?> folder() async => null;
}

/// Android without the access to all files, until it is asked.
class _Storage implements ReceiveStorage {
  bool granted = false;
  int asked = 0;
  @override
  bool get supported => true;
  @override
  Future<bool> allFiles() async => granted;
  @override
  Future<bool> askAllFiles() async {
    asked++;
    return granted = true;
  }

  @override
  Future<String> root() async => '/storage/emulated/0/medXfer';
}

Map<String, Object?> _q(int i, String path, int size, String status) => {'index': i, 'rel_path': path, 'size': size, 'status': status};

Future<(FakeCore, AppState)> _app(
  WidgetTester tester,
  FakeCore core, {
  FilePicker picker = const NoFilePicker(),
  ReceiveStorage storage = const NoReceiveStorage(),
}) async {
  core.on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'Galaxy A57'})]));
  final state = AppState(core: core, folders: const _TestFolders(), filePicker: picker, storage: storage, history: MemoryHistoryStore());
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
  testWidgets('an offer comes in: accepted, followed file by file, one skipped, then in the history', (tester) async {
    final core = FakeCore()
      ..on('respond_offer', (_) => const FakeAnswer())
      ..on('skip_file', (_) => const FakeAnswer());
    await _app(tester, core);
    core.emit(CoreEvent('incoming_offer', data: {
      'device_name': 'PC du salon',
      'file_name': 'Photos',
      'file_size': 3 * 1048576,
      'total_files': 3,
      'is_batch': true,
      'items': [for (var i = 0; i < 3; i++) _q(i, 'Photos/${i + 1}.jpg', 1048576, 'pending')],
      'free_bytes': 1048576,
      'enough_space': false,
    }));
    await tester.pumpAndSettle();
    expect(find.text('PC du salon veut vous envoyer'), findsOneWidget);
    expect(find.text('3 fichiers · 3 Mo'), findsOneWidget);
    expect(find.text('Pas assez de place : il faut 3 Mo, il reste 1 Mo.'), findsOneWidget);

    await tester.tap(find.byKey(const Key('offer-accept')));
    await tester.pumpAndSettle();
    // The receive folder goes with every acceptance: the default one here (WP-B.10).
    expect(core.calls.last.payload, {'accept': true, 'save_dir': '/tmp/medxfer-test/out'});
    expect(find.text('Réception de PC du salon'), findsOneWidget);
    expect(find.text('Photos/3.jpg'), findsOneWidget);

    core.emit(CoreEvent('transfer_progress', data: {
      'batch_bytes': 1048576,
      'batch_total_bytes': 3 * 1048576,
      'speed_mbps': 8.0,
      'eta_seconds': 75,
      'items': [
        {'index': 0, 'bytes': 1048576, 'size': 1048576},
      ],
    }));
    await tester.pump();
    await tester.pump();
    expect(find.text('8 Mo/s · encore 1 min 15 s'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('skip-2')));
    await tester.pump();
    expect(core.calls.last.action, 'skip_file');
    expect(core.calls.last.payload, {'item_index': 2});

    core.emit(CoreEvent('transfer_complete', data: {
      'items': [_q(0, 'Photos/1.jpg', 1048576, 'completed'), _q(1, 'Photos/2.jpg', 1048576, 'completed'), _q(2, 'Photos/3.jpg', 1048576, 'skipped')],
    }));
    await tester.pumpAndSettle();
    expect(find.text('Terminé, avec des fichiers passés ou en échec'), findsOneWidget);
    await tester.tap(find.byKey(const Key('transfer-close')));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Historique'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('history-0')), findsOneWidget);
    expect(find.textContaining('Reçu de PC du salon'), findsOneWidget);
  });

  testWidgets('declined: answered no, nothing opened', (tester) async {
    final core = FakeCore()..on('respond_offer', (_) => const FakeAnswer());
    await _app(tester, core);
    core.emit(const CoreEvent('incoming_offer', data: {'device_name': 'PC', 'file_name': 'a.txt', 'file_size': 12, 'total_files': 1, 'enough_space': true}));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('offer-decline')));
    await tester.pumpAndSettle();
    expect(core.calls.last.payload, {'accept': false});
    expect(find.text('PC veut vous envoyer'), findsNothing);
    expect(find.byKey(const Key('transfer-items')), findsNothing);
  });

  testWidgets('a desktop sends what it picked; declined by the other side', (tester) async {
    final core = FakeCore()
      ..on('send', (p) => const FakeAnswer(broadcast: [CoreEvent('file_offered', data: {'file_name': 'rapport.pdf', 'file_size': 2048})]));
    await _app(tester, core, picker: const _Picker([r'C:\Users\a\rapport.pdf']));
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Galaxy'}));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('send-files')));
    // The waiting bar moves until the other side answers: no settling, a few frames.
    for (var i = 0; i < 10; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    expect(core.calls.last.action, 'send');
    expect(core.calls.last.payload, {
      'paths': [r'C:\Users\a\rapport.pdf'],
    });
    expect(find.text('Envoi à Galaxy'), findsOneWidget);
    expect(find.text("En attente de l'accord de Galaxy…"), findsOneWidget);

    core.emit(const CoreEvent('transfer_rejected', data: {'message': 'Transfer rejected by user'}));
    await tester.pumpAndSettle();
    expect(find.text('Refusé'), findsOneWidget);
  });

  testWidgets('WP-B.8: a send stopped half way is resumed from its screen, the same files again', (tester) async {
    final core = FakeCore()
      ..on('send', (p) => const FakeAnswer(broadcast: [CoreEvent('file_offered', data: {'file_name': 'film.mkv', 'file_size': 4096})]));
    await _app(tester, core, picker: const _Picker([r'C:\Users\a\film.mkv']));
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Galaxy'}));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('send-files')));
    for (var i = 0; i < 10; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    core.emit(const CoreEvent('transfer_accepted', data: {}));
    core.emit(const CoreEvent('transfer_failed', data: {'error': 'no data connection', 'by': 'peer'}));
    await tester.pumpAndSettle();
    expect(find.text('Transfert interrompu'), findsOneWidget);

    // The session not back yet: refused, the stopped transfer stays, with the reason.
    core.on('send', (p) => const FakeAnswer(ok: false, replies: [CoreEvent('action_error', data: {'error': 'Not paired with any device.'})]));
    await tester.tap(find.byKey(const Key('transfer-resend')));
    for (var i = 0; i < 10; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    expect(find.text('Transfert interrompu'), findsOneWidget);
    expect(find.byKey(const Key('transfer-resend')), findsOneWidget);

    // Back: sent again, the new offer takes its place.
    core.on('send', (p) => const FakeAnswer(broadcast: [CoreEvent('file_offered', data: {'file_name': 'film.mkv', 'file_size': 4096})]));
    await tester.tap(find.byKey(const Key('transfer-resend')));
    for (var i = 0; i < 10; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    expect(core.calls.where((c) => c.action == 'send').map((c) => c.payload), [
      {'paths': [r'C:\Users\a\film.mkv']},
      {'paths': [r'C:\Users\a\film.mkv']},
      {'paths': [r'C:\Users\a\film.mkv']},
    ]);
    expect(find.text("En attente de l'accord de Galaxy…"), findsOneWidget);
  });

  testWidgets('WP-B.8: a transfer gone while its screen is open closes the screen, not a blank one', (tester) async {
    final core = FakeCore()
      ..on('send', (p) => const FakeAnswer(broadcast: [CoreEvent('file_offered', data: {'file_name': 'film.mkv', 'file_size': 4096})]));
    await _app(tester, core, picker: const _Picker([r'C:\Users\a\film.mkv']));
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Galaxy'}));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('send-files')));
    for (var i = 0; i < 10; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    expect(find.text('Envoi à Galaxy'), findsOneWidget);
    // The offer refused by the core, later, as an error of the send call: the transfer is gone.
    final sendId = 'ui-${core.calls.indexWhere((c) => c.action == 'send') + 1}';
    core.emit(CoreEvent('action_error', id: sendId, data: const {'error': 'Not paired with any device.'}));
    await tester.pumpAndSettle();
    expect(find.text('Envoi à Galaxy'), findsNothing);
    expect(find.byKey(const Key('bottom-bar')), findsOneWidget);
  });

  testWidgets('WP-B.8: the session lost during a transfer, Connect is there next to it', (tester) async {
    final core = FakeCore();
    await _app(tester, core);
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Galaxy'}));
    core.emit(const CoreEvent('file_offered', data: {'file_name': 'film.mkv', 'file_size': 4096}));
    core.emit(const CoreEvent('transfer_accepted', data: {}));
    await tester.pumpAndSettle();
    core.emit(const CoreEvent('disconnected', data: {'reason': 'connection timed out'}));
    await tester.pumpAndSettle();
    // The transfer screen, open since the send, says it waits.
    expect(find.text('Connexion perdue avec Galaxy : en attente'), findsOneWidget);
    // The data goes on without the session: the screen says so.
    core.emit(const CoreEvent('transfer_progress', data: {'batch_bytes': 2048, 'batch_total_bytes': 4096, 'speed_mbps': 12.5}));
    await tester.pumpAndSettle();
    expect(find.text('Connexion perdue avec Galaxy : le transfert continue'), findsOneWidget);
    await tester.binding.handlePopRoute(); // the system back
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('see-transfer')), findsOneWidget);
    expect(find.byKey(const Key('connect')), findsOneWidget);
  });

  testWidgets('a running transfer left with back stays closed; the bottom bar brings it back', (tester) async {
    final core = FakeCore();
    await _app(tester, core);
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Galaxy'}));
    core.emit(const CoreEvent('file_offered', data: {'file_name': 'film.mkv', 'file_size': 4096}));
    core.emit(const CoreEvent('transfer_accepted', data: {}));
    await tester.pumpAndSettle();
    expect(find.text('Envoi à Galaxy'), findsOneWidget);

    // pageBack() looks for the English tooltip; this screen speaks French.
    await tester.tap(find.byType(BackButton));
    await tester.pumpAndSettle();
    core.emit(const CoreEvent('transfer_progress', data: {'batch_bytes': 1024, 'batch_total_bytes': 4096, 'speed_mbps': 1.0}));
    await tester.pumpAndSettle();
    expect(find.text('Envoi à Galaxy'), findsNothing);

    await tester.tap(find.byKey(const Key('see-transfer')));
    await tester.pumpAndSettle();
    expect(find.text('Envoi à Galaxy'), findsOneWidget);
  });

  testWidgets('Android without the access to all files: said in the offer, allowed there, then sorted', (tester) async {
    final core = FakeCore()..on('respond_offer', (_) => const FakeAnswer());
    final storage = _Storage();
    await _app(tester, core, storage: storage);
    core.emit(CoreEvent('incoming_offer', data: {
      'device_name': 'PC',
      'file_name': 'Vacances',
      'file_size': 2,
      'total_files': 2,
      'enough_space': true,
      'items': [_q(0, 'Vacances/a.jpg', 1, 'pending'), _q(1, 'Vacances/b.jpg', 1, 'pending')],
    }));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('storage-hint')), findsOneWidget);
    await tester.tap(find.byKey(const Key('storage-allow')));
    await tester.pumpAndSettle();
    expect(storage.asked, 1);
    expect(find.byKey(const Key('storage-hint')), findsNothing);
    await tester.tap(find.byKey(const Key('offer-accept')));
    await tester.pumpAndSettle();
    expect(core.calls.last.payload, {'accept': true, 'save_dir': '/storage/emulated/0/medXfer/Photos'});
  });
}
