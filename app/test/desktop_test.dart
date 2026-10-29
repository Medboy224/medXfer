// The desktop layout (mockup « 10. Bureau ») on the fake core: the side menu and its pages,
// files and folders chosen together, and the connection beside them (WP-B.8, 27 October).
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/connection/connection.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/platform/file_picker.dart';
import 'package:medxfer/platform/folders.dart';
import 'package:medxfer/platform/hotspot.dart';
import 'package:medxfer/platform/network.dart';
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

/// The network a computer creates (WP-C.2 of the J6c plan), as WindowsHotspot gives it.
class _PcHotspot implements Hotspot {
  _PcHotspot({this.leavesWifi = false, this.dropsClients = false});

  final log = <String>[];

  @override
  bool get supported => true;

  @override
  bool get canJoin => false;

  /// Linux: the Wi-Fi it was on is left while the network runs.
  @override
  final bool leavesWifi;

  /// Windows without its connection sharing: the phones dropped every 2 minutes.
  final bool dropsClients;

  @override
  Future<bool> permission() async => true;

  @override
  Future<HotspotInfo> start() async {
    log.add('start');
    return HotspotInfo(
      ssid: 'DIRECT-AB-medXfer',
      passphrase: 'K7XQ2MPRT9ZD',
      security: 1,
      address: '192.168.137.1',
      net: const NetInterface(name: 'Connexion au réseau local* 2', index: 23, addrs: ['192.168.137.1/24']),
      dropsClients: dropsClients,
      addressHeldBy: dropsClients ? 'Ethernet' : null,
      sharingAddress: dropsClients ? '192.168.137.1' : null,
    );
  }

  @override
  Future<void> stop() async => log.add('stop');

  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1}) => throw UnsupportedError('no');

  @override
  Future<void> leave() async {}

  @override
  Future<int> joinedNetwork() async => 0;
}

Future<AppState> _app(WidgetTester tester, FakeCore core, {Hotspot hotspot = const NoHotspot()}) async {
  final state = AppState(
    core: core,
    folders: const _TestFolders(),
    filePicker: const _Picker([_file], _folder),
    history: MemoryHistoryStore(),
    desktop: true,
    hotspot: hotspot,
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

    expect(find.byKey(const Key('nav-receive')), findsNothing, reason: 'no page Recevoir: its folder is in the settings');
    await tester.tap(find.byKey(const Key('nav-history')));
    await tester.pumpAndSettle();
    expect(find.text('Aucun transfert pour le moment.'), findsOneWidget);
    await tester.tap(find.byKey(const Key('nav-trusted')));
    await tester.pumpAndSettle();
    expect(find.text('Aucun appareil de confiance pour le moment.'), findsOneWidget);
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pumpAndSettle();
    expect(find.text('Dossier de réception'), findsOneWidget);
    expect(find.byKey(const Key('dev-mode')), findsOneWidget);
    await tester.tap(find.byKey(const Key('nav-home')));
    await tester.pumpAndSettle();
    expect(find.text('Accueil'), findsOneWidget);
    expect(find.byKey(const Key('drop-card')), findsOneWidget);
  });

  testWidgets('27 Oct.: the window open, this computer is seen by its network, whatever the page', (tester) async {
    final core = _core();
    await _app(tester, core);
    expect(core.calls.where((c) => c.action == 'set_discoverable').last.payload, {'on': true});
    expect(core.calls.where((c) => c.action == 'scan'), isNotEmpty);
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('nav-home')));
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

  testWidgets('WP-C.3: this computer creates its network; the session over, it stops', (tester) async {
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_show', (_) => const FakeAnswer(replies: [
            CoreEvent('pair_show', data: {'uri': 'medxfer://v1/pair?fp=AAAA&a=192.168.137.1&p=18887&s=x&exp=1900000000', 'expires': 1900000000, 'instance': 'medXfer-7810'}),
          ]))
      ..on('pair_pin_show', (_) => const FakeAnswer(replies: [
            CoreEvent('pair_pin_shown', data: {'pin': '054 461', 'expires': 1900000000, 'address': '192.168.137.1:18887', 'instance': 'medXfer-7810'}),
          ]));
    final h = _PcHotspot();
    await _app(tester, core, hotspot: h);
    expect(find.byKey(const Key('hotspot-leaves-wifi')), findsNothing);
    await tester.tap(find.byKey(const Key('hotspot-start')));
    await _frames(tester);
    expect(h.log, ['start']);
    expect(core.calls.where((c) => c.action == 'set_network_info').last.payload, {
      'interfaces': [
        {'name': 'Connexion au réseau local* 2', 'index': 23, 'addrs': ['192.168.137.1/24']},
      ],
    });
    expect(find.text('Point d\'accès actif'), findsOneWidget);
    expect(find.byKey(const Key('hotspot-unstable')), findsNothing);

    // The phone scanned the QR code: paired, the screen closes, the network stays for the session.
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.137.20', 'device_name': 'Galaxy A57'}));
    await _frames(tester);
    expect(find.text('Point d\'accès actif'), findsNothing);
    expect(h.log, ['start']);
    core.emit(const CoreEvent('disconnected', data: {'reason': 'peer requested disconnect'}));
    await _frames(tester);
    expect(h.log, ['start', 'stop']);
    expect(core.calls.where((c) => c.action == 'set_network_info').last.payload, {'enumerate': true});
  });

  testWidgets('J6c: under Linux, the Wi-Fi left while the network runs is said before and during', (tester) async {
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_show', (_) => const FakeAnswer())
      ..on('pair_pin_show', (_) => const FakeAnswer());
    await _app(tester, core, hotspot: _PcHotspot(leavesWifi: true));
    expect(find.byKey(const Key('hotspot-leaves-wifi')), findsOneWidget);
    await tester.tap(find.byKey(const Key('hotspot-start')));
    await _frames(tester);
    expect(find.text("Wi-Fi de ce PC quitté le temps du partage, retrouvé à l'arrêt"), findsOneWidget);
  });

  testWidgets('WP-C.5: Windows without its connection sharing says the phones will be dropped, and why', (tester) async {
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_show', (_) => const FakeAnswer(replies: [
            CoreEvent('pair_show', data: {'uri': 'medxfer://v1/pair?fp=AAAA&a=169.254.121.148&p=18887&s=x&exp=1900000000', 'expires': 1900000000, 'instance': 'medXfer-7810'}),
          ]))
      ..on('pair_pin_show', (_) => const FakeAnswer(replies: [
            CoreEvent('pair_pin_shown', data: {'pin': '054 461', 'expires': 1900000000, 'address': '169.254.121.148:18887', 'instance': 'medXfer-7810'}),
          ]));
    await _app(tester, core, hotspot: _PcHotspot(dropsClients: true));
    await tester.tap(find.byKey(const Key('hotspot-start')));
    await _frames(tester);
    final warning = find.byKey(const Key('hotspot-unstable'));
    expect(warning, findsOneWidget);
    expect(find.descendant(of: warning, matching: find.textContaining("« Ethernet » détient l'adresse 192.168.137.1")), findsOneWidget);
  });

  testWidgets("J6c: a session lost keeps this computer's network for the device to come back; then it stops", (tester) async {
    final old = Connection.rejoinFor;
    Connection.rejoinFor = const Duration(seconds: 30);
    addTearDown(() => Connection.rejoinFor = old);
    final core = _core()
      ..on('set_network_info', (_) => const FakeAnswer())
      ..on('pair_show', (_) => const FakeAnswer())
      ..on('pair_pin_show', (_) => const FakeAnswer());
    final h = _PcHotspot();
    await _app(tester, core, hotspot: h);
    await tester.tap(find.byKey(const Key('hotspot-start')));
    await _frames(tester);
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.137.20', 'device_name': 'Galaxy A57'}));
    await _frames(tester);
    // The session timed out (the phone dropped by Windows, not back within 15 s): kept.
    core.emit(const CoreEvent('reconnecting', data: {}));
    core.emit(const CoreEvent('disconnected', data: {'reason': 'connection timed out'}));
    await _frames(tester);
    expect(h.log, ['start'], reason: 'stopped, the phone had no network to come back to');
    // Back: the session, and the network stays.
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.137.20', 'device_name': 'Galaxy A57'}));
    await tester.pump(const Duration(seconds: 40));
    expect(h.log, ['start']);
    // Lost again, not back: stopped after rejoinFor.
    core.emit(const CoreEvent('disconnected', data: {'reason': 'connection timed out'}));
    await tester.pump(const Duration(seconds: 31));
    expect(h.log, ['start', 'stop']);
  });
}
