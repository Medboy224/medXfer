// The screens of WP-B.2 on the fake core: Connect, the session banner, what the other device
// asks of this user, the refusal of ID-05, the trusted devices.
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/features/connect/connect_screen.dart';
import 'package:medxfer/platform/folders.dart';
import 'package:medxfer/platform/hotspot.dart';
import 'package:medxfer/platform/qr_scanner.dart';
import 'package:medxfer/settings.dart';
import 'package:medxfer/transfer/history.dart';

const _expires = 1900000000;

/// Scrolls the Connect screen until finder is built, then on screen: its list builds lazily,
/// and the home screen's scrollables stay below it.
Future<void> _reveal(WidgetTester tester, Finder finder) async {
  final list = find.descendant(of: find.byType(ConnectScreen), matching: find.byType(Scrollable)).first;
  await tester.scrollUntilVisible(finder, 200, scrollable: list);
  await tester.ensureVisible(finder);
  await tester.pumpAndSettle();
}

class _TestFolders implements Folders {
  const _TestFolders();
  @override
  Future<String> dataDir() async => '/tmp/medxfer-test/data';
  @override
  Future<String> downloadDir() async => '/tmp/medxfer-test/out';
}

FakeCore _core() => FakeCore()
  ..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'Galaxy A57'})]))
  ..on('set_discoverable', (_) => const FakeAnswer())
  ..on('pair_show', (_) => const FakeAnswer(replies: [
        CoreEvent('pair_show', data: {'uri': 'medxfer://v1/pair?fp=AAAA&a=192.168.1.20&p=18887&s=x&exp=$_expires', 'expires': _expires, 'instance': 'medXfer-7810'}),
      ]))
  ..on('pair_pin_show', (_) => const FakeAnswer(replies: [
        CoreEvent('pair_pin_shown', data: {'pin': '054 461', 'expires': _expires, 'address': '192.168.1.20:18887', 'instance': 'medXfer-7810'}),
      ]))
  ..on('scan', (_) => const FakeAnswer(replies: [
        CoreEvent('peers_list', data: [
          {'device_name': 'medXfer-1A2B', 'host_ip': '192.168.1.30', 'port': 18887},
        ]),
      ]));

/// A hotspot that starts at once.
class _Hotspot implements Hotspot {
  @override
  bool get supported => true;
  @override
  Future<bool> permission() async => true;
  @override
  Future<HotspotInfo> start() async => const HotspotInfo(ssid: 'AndroidShare_1732', passphrase: 'vvrtsy', security: 2, address: '10.46.56.232');
  @override
  Future<void> stop() async {}
  @override
  Future<HotspotInfo> join(String ssid, String passphrase, {int security = 1}) async => const HotspotInfo();
  @override
  Future<void> leave() async {}
}

/// A camera that reads [text] at once.
class _Scanner implements QrScanner {
  const _Scanner(this.text);
  final String text;
  @override
  bool get supported => true;
  @override
  Future<String?> scan(BuildContext context) async => text;
}

Future<(FakeCore, AppState)> _app(WidgetTester tester, FakeCore core, {Hotspot hotspot = const NoHotspot(), QrScanner scanner = const NoQrScanner()}) async {
  final state = AppState(core: core, folders: const _TestFolders(), hotspot: hotspot, qrScanner: scanner, history: MemoryHistoryStore());
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
  testWidgets('Connect: the code, a device of the list, its PIN, then the banner', (tester) async {
    final core = _core()
      ..on('pair', (_) => const FakeAnswer(broadcast: [CoreEvent('pin_needed', data: {'target': '192.168.1.30:18887'})]))
      ..on('pair_pin', (_) => const FakeAnswer(broadcast: [
            CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Pixel 8', 'fingerprint': 'BDIT-4HBW', 'mutual': false}),
          ]))
      ..on('disconnect', (_) => const FakeAnswer(broadcast: [CoreEvent('disconnected', data: {'reason': 'user requested disconnect'})]));
    await _app(tester, core);

    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('my-qr')), findsOneWidget);
    expect(find.text('054 461'), findsOneWidget);
    expect(find.text('Apparaît comme medXfer-7810'), findsOneWidget);
    expect(core.calls.firstWhere((c) => c.action == 'set_discoverable').payload, {'on': true});

    await _reveal(tester, find.text('medXfer-1A2B'));
    await tester.tap(find.text('medXfer-1A2B'));
    await tester.pumpAndSettle();
    expect(find.text('Tapez le PIN affiché sur medXfer-1A2B.'), findsOneWidget);
    await tester.enterText(find.byKey(const Key('pin-field')), '054 461');
    await tester.pump();
    await tester.tap(find.byKey(const Key('pin-ok')));
    await tester.pumpAndSettle();

    expect(core.calls.firstWhere((c) => c.action == 'pair_pin').payload, {'target': '192.168.1.30:18887', 'pin': '054 461', 'trust': true});
    // Paired: the Connect screen closed, this device stopped announcing itself.
    expect(find.byKey(const Key('my-qr')), findsNothing);
    expect(core.calls.last.action, 'set_discoverable');
    expect(core.calls.last.payload, {'on': false});
    expect(find.byKey(const Key('session-banner')), findsOneWidget);
    expect(find.text('Connecté à Pixel 8'), findsWidgets);

    await tester.tap(find.byKey(const Key('disconnect')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('session-banner')), findsNothing);
    expect(find.byKey(const Key('connect')), findsOneWidget);
  });

  testWidgets('Connect: a PIN typed with the address of the other device', (tester) async {
    final core = _core()..on('pair_pin', (_) => const FakeAnswer());
    await _app(tester, core);
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();
    await _reveal(tester, find.byKey(const Key('type-pin')));
    await tester.tap(find.byKey(const Key('type-pin')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('pin-field')), '054461');
    await tester.pump();
    // No address yet: the button waits.
    expect(tester.widget<FilledButton>(find.byKey(const Key('pin-ok'))).onPressed, isNull);
    await tester.enterText(find.byKey(const Key('pin-address')), '192.168.1.30');
    await tester.pump();
    await tester.tap(find.byKey(const Key('pin-ok')));
    await tester.pumpAndSettle();
    expect(core.calls.last.payload, {'target': '192.168.1.30', 'pin': '054461', 'trust': true});
  });

  testWidgets('a device asks for a PIN: shown on any screen, gone once paired', (tester) async {
    final core = _core();
    final (_, state) = await _app(tester, core);
    core.emit(const CoreEvent('pin_requested', data: {'pin': '123 456', 'expires': _expires, 'from': '192.168.1.40'}));
    await tester.pumpAndSettle();
    expect(find.text('Un appareil veut se connecter'), findsOneWidget);
    expect(find.text('123 456'), findsOneWidget);
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.40', 'device_name': 'PC du salon'}));
    await tester.pumpAndSettle();
    expect(find.text('123 456'), findsNothing);
    expect(state.connection.session!.name, 'PC du salon');
  });

  testWidgets('mutual trust offered: the key to compare, then the answer', (tester) async {
    final core = _core()..on('trust_answer', (_) => const FakeAnswer());
    await _app(tester, core);
    core.emit(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'Pixel 8'}));
    core.emit(const CoreEvent('trust_offered', data: {'device_name': 'Pixel 8', 'fingerprint': 'BDIT-4HBW', 'timeout_s': 60}));
    await tester.pumpAndSettle();
    expect(find.text('Faire confiance à Pixel 8 ?'), findsOneWidget);
    expect(find.textContaining('BDIT-4HBW'), findsOneWidget);
    await tester.tap(find.byKey(const Key('trust-accept')));
    await tester.pumpAndSettle();
    expect(core.calls.last.action, 'trust_answer');
    expect(core.calls.last.payload, {'accept': true});
    expect(find.text('Faire confiance à Pixel 8 ?'), findsNothing);
  });

  testWidgets('a trust offer left unanswered goes with its deadline', (tester) async {
    final core = _core();
    final (_, state) = await _app(tester, core);
    core.emit(const CoreEvent('trust_offered', data: {'device_name': 'Pixel 8', 'fingerprint': 'BDIT-4HBW', 'timeout_s': 2}));
    await tester.pumpAndSettle();
    expect(find.text('Faire confiance à Pixel 8 ?'), findsOneWidget);
    await tester.pump(const Duration(seconds: 3));
    await tester.pumpAndSettle();
    expect(find.text('Faire confiance à Pixel 8 ?'), findsNothing);
    expect(state.connection.trustOffer, isNull);
  });

  testWidgets('a known device with another key: refused, said plainly (ID-05)', (tester) async {
    final core = _core();
    await _app(tester, core);
    core.emit(const CoreEvent('action_error', id: 'ui-9', data: {'error': "failed to pair with 192.168.1.30:18887: REFUSED: 192.168.1.30 is the address of 'Bureau'"}));
    await tester.pumpAndSettle();
    expect(find.text('Connexion refusée'), findsOneWidget);
    expect(find.text("192.168.1.30 is the address of 'Bureau'"), findsOneWidget);
  });

  testWidgets('the trusted devices: listed, renamed', (tester) async {
    final core = _core()
      ..on('trust_list', (_) => const FakeAnswer(replies: [
            CoreEvent('trusted_devices', data: {
              'devices': [
                {'fingerprint': 'BDIT4HBWIMJS', 'short': 'BDIT-4HBW', 'alias': 'Pixel 8', 'last_address': '192.168.1.30'},
              ],
            }),
          ]))
      ..on('trust_rename', (p) => FakeAnswer(replies: [
            CoreEvent('trusted_devices', data: {
              'devices': [
                {'fingerprint': 'BDIT4HBWIMJS', 'short': 'BDIT-4HBW', 'alias': p['name'], 'last_address': '192.168.1.30'},
              ],
            }),
          ]));
    await _app(tester, core);
    await tester.tap(find.byKey(const Key('home-menu')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Appareils de confiance'));
    await tester.pumpAndSettle();
    expect(find.text('Pixel 8'), findsOneWidget);
    expect(find.text('Clé BDIT-4HBW · 192.168.1.30'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('trusted-menu-BDIT4HBWIMJS')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Renommer'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('rename-field')), 'Téléphone de Awa');
    await tester.tap(find.byKey(const Key('rename-ok')));
    await tester.pumpAndSettle();
    expect(core.calls.last.payload, {'fingerprint': 'BDIT4HBWIMJS', 'name': 'Téléphone de Awa'});
    expect(find.text('Téléphone de Awa'), findsOneWidget);
  });

  testWidgets('no common Wi-Fi: the access point, its name and password for a device without camera', (tester) async {
    final core = _core();
    await _app(tester, core, hotspot: _Hotspot());
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();
    await _reveal(tester, find.byKey(const Key('hotspot-start')));
    await tester.tap(find.byKey(const Key('hotspot-start')));
    await tester.pumpAndSettle();
    expect(find.text('Réseau : AndroidShare_1732'), findsOneWidget);
    expect(find.text('Mot de passe : vvrtsy'), findsOneWidget);
    expect(find.byKey(const Key('hotspot-stop')), findsOneWidget);
  });

  testWidgets('the camera reads a link: paired by it', (tester) async {
    const link = 'medxfer://v1/pair?fp=AAAA&a=192.168.1.30&p=18887&s=x&exp=1900000000';
    final core = _core()..on('pair_uri', (_) => const FakeAnswer());
    await _app(tester, core, scanner: const _Scanner(link));
    await tester.tap(find.byTooltip('Scanner un QR pour rejoindre'));
    await tester.pumpAndSettle();
    expect(core.calls.last.action, 'pair_uri');
    expect(core.calls.last.payload['uri'], link);
  });

  testWidgets('a desktop pastes a link that names a network: told to join it first', (tester) async {
    const link = 'medxfer://v1/pair?fp=AAAA&a=10.46.56.232&p=18887&s=x&exp=1900000000&ws=AndroidShare_1732&wp=vvrtsy&wt=2';
    final core = _core()..on('pair_uri', (_) => const FakeAnswer());
    // The link dialog reads the clipboard first: empty here.
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async => call.method == 'Clipboard.getData' ? {'text': ''} : null);
    addTearDown(() => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, null));
    await _app(tester, core);
    await tester.tap(find.byTooltip('Scanner un QR pour rejoindre'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('link-field')), link);
    await tester.pump();
    await tester.tap(find.byKey(const Key('link-ok')));
    await tester.pumpAndSettle();
    expect(find.textContaining('AndroidShare_1732'), findsOneWidget);
    expect(find.textContaining('vvrtsy'), findsOneWidget);
    expect(core.calls.where((c) => c.action == 'pair_uri'), isEmpty);
    await tester.tap(find.byKey(const Key('join-wifi-ok')));
    await tester.pumpAndSettle();
    expect(core.calls.last.payload['uri'], link);
  });
}
