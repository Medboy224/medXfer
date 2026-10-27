// The desktop layout (mockup « 10. Bureau ») on the fake core: the side menu and its pages,
// files and folders chosen together, and the connection beside them (WP-B.8, 27 October).
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

class _TestFolders implements Folders {
  const _TestFolders();
  @override
  Future<String> dataDir() async => '/tmp/medxfer-test/data';
  @override
  Future<String> downloadDir() async => '/tmp/medxfer-test/out';
}

/// The system's dialogs: these files, then this folder, each time.
class _Picker implements FilePicker {
  const _Picker(this.paths, this.folderPath);
  final List<String> paths;
  final String folderPath;
  @override
  bool get supported => true;
  @override
  bool get folders => true;
  @override
  Future<Picked> files() async => Picked(paths: paths);
  @override
  Future<String?> folder() async => folderPath;
}

const _file = '/home/a/rapport.pdf';
const _folder = '/home/a/Vacances 2026';

FakeCore _core() => FakeCore()
  ..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'PC Naruto'})]))
  ..on('scan', (_) => const FakeAnswer(replies: [
        CoreEvent('peers_list', data: [
          {'device_name': 'medXfer-1A2B', 'trusted_alias': 'Galaxy A57', 'host_ip': '192.168.1.30', 'port': 18887},
          {'device_name': 'medXfer-91c0', 'host_ip': '192.168.1.40', 'port': 18887},
        ]),
      ]))
  ..on('send', (_) => const FakeAnswer(broadcast: [CoreEvent('file_offered', data: {'file_name': 'rapport.pdf', 'file_size': 2048})]));

Future<AppState> _app(WidgetTester tester, FakeCore core) async {
  final state = AppState(
    core: core,
    folders: const _TestFolders(),
    filePicker: const _Picker([_file], _folder),
    history: MemoryHistoryStore(),
    desktop: true,
  );
  tester.platformDispatcher.localesTestValue = [const Locale('fr')];
  addTearDown(tester.platformDispatcher.clearLocalesTestValue);
  tester.view.physicalSize = const Size(1440, 900);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MedxferApp(state: state, settings: AppSettings()));
  await state.start();
  await tester.pumpAndSettle();
  return state;
}

/// Pumps a few frames: a transfer screen's waiting bar never settles.
Future<void> _frames(WidgetTester tester) async {
  for (var i = 0; i < 10; i++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}

void main() {
  testWidgets('the side menu: this computer, and its pages', (tester) async {
    await _app(tester, _core());
    expect(find.byKey(const Key('side-menu')), findsOneWidget);
    expect(find.text('Cet appareil : PC Naruto'), findsOneWidget);
    expect(find.text('Déposez ici des fichiers ou des dossiers'), findsOneWidget);
    expect(find.text('Connexion'), findsOneWidget);

    await tester.tap(find.byKey(const Key('nav-receive')));
    await tester.pumpAndSettle();
    expect(find.text('Dossier de réception'), findsOneWidget);
    await tester.tap(find.byKey(const Key('nav-history')));
    await tester.pumpAndSettle();
    expect(find.text('Aucun transfert pour le moment.'), findsOneWidget);
    await tester.tap(find.byKey(const Key('nav-trusted')));
    await tester.pumpAndSettle();
    expect(find.text('Aucun appareil de confiance pour le moment.'), findsOneWidget);
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('dev-mode')), findsOneWidget);
  });

  testWidgets('27 Oct.: the window open, this computer is seen by its network, whatever the page', (tester) async {
    final core = _core();
    await _app(tester, core);
    expect(core.calls.where((c) => c.action == 'set_discoverable').last.payload, {'on': true});
    expect(core.calls.where((c) => c.action == 'scan'), isNotEmpty);
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('nav-receive')));
    await tester.pumpAndSettle();
    expect(core.calls.where((c) => c.action == 'set_discoverable').last.payload, {'on': true}, reason: 'a page is no reason to hide');
  });

  testWidgets('WP-B.8: files and a folder, chosen one after the other, go to the connected device in one transfer', (tester) async {
    final core = _core();
    await _app(tester, core);
    await tester.tap(find.byKey(const Key('pick-files')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('pick-folder')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('pick-files'))); // chosen again: still once
    await tester.pumpAndSettle();
    expect(find.textContaining('2 éléments'), findsOneWidget);
    expect(find.text('rapport.pdf'), findsOneWidget);
    expect(find.text('Vacances 2026'), findsOneWidget);
    // Not connected: nothing to send to yet.
    expect(find.byKey(const Key('send')), findsNothing);
    expect(find.byKey(const Key('connect-to-send')), findsOneWidget);

    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Galaxy A57'}));
    await tester.pumpAndSettle();
    expect(find.text('Envoyer à Galaxy A57'), findsOneWidget);
    expect(core.calls.where((c) => c.action == 'send'), isEmpty, reason: 'connected, not sent yet');
    await tester.tap(find.byKey(const Key('send')));
    await _frames(tester);
    expect(core.calls.where((c) => c.action == 'send').single.payload, {
      'paths': [_file, _folder],
    });
    expect(find.byKey(const Key('basket')), findsNothing, reason: 'the list is emptied once sent');
  });

  testWidgets('27 Oct.: the devices of the same network are connected, not sent to; one not trusted asks its PIN', (tester) async {
    final core = _core()..on('pair', (_) => const FakeAnswer(broadcast: [CoreEvent('pin_needed', data: {'target': '192.168.1.40:18887'})]));
    await _app(tester, core);
    expect(find.text('De confiance · connexion sans code'), findsOneWidget);
    expect(find.text('Il affichera un PIN à taper ici'), findsOneWidget);

    await tester.tap(find.byKey(const Key('pick-files')));
    await tester.pumpAndSettle();
    await tester.tap(find.descendant(of: find.byKey(const ValueKey('device-192.168.1.40')), matching: find.byType(FilledButton)));
    await tester.pumpAndSettle();
    expect(core.calls.where((c) => c.action == 'pair').single.payload, containsPair('ip', '192.168.1.40'));
    expect(find.text('medXfer-91c0'), findsWidgets, reason: 'the PIN window names the device');
    expect(core.calls.where((c) => c.action == 'send'), isEmpty, reason: 'connecting sends nothing');
  });

  testWidgets('one item removed, then all', (tester) async {
    await _app(tester, _core());
    await tester.tap(find.byKey(const Key('pick-files')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('pick-folder')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('basket-remove-0')));
    await tester.pumpAndSettle();
    expect(find.text('rapport.pdf'), findsNothing);
    expect(find.text('Vacances 2026'), findsOneWidget);
    await tester.tap(find.byKey(const Key('basket-clear')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('basket')), findsNothing);
  });
}
