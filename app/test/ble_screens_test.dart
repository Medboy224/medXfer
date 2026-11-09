// The screens of the path by BLE (WP-D.5 of the J6d plan) on the fake core and a fake radio: the
// permission's reason before its dialog, the devices found by Bluetooth in the Connect list, the
// PIN of an unknown one, and a refusal said.
import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/connection/ble_link.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/features/connect/connect_screen.dart';
import 'package:medxfer/platform/ble.dart';
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

/// The radio: the permission granted or not, the devices it sees.
class _Radio implements Ble {
  final _events = StreamController<BleEvent>.broadcast();
  bool allowed = true, started = false;
  bool canAdvertise = true; // no peripheral role: Windows says no_advertiser
  int asked = 0;
  bool dropping = false; // out of reach: each exchange fails

  void add(BleEvent e) => _events.add(e);

  @override
  bool get supported => true;
  @override
  Future<bool> enabled() async => true;
  @override
  Future<bool> permission() async {
    asked++;
    return allowed = true; // the user accepts the system's dialog
  }

  @override
  Future<bool> granted() async => allowed;
  @override
  Future<bool> turnOn() async => true;
  @override
  Future<void> start({bool advertise = true, bool scan = true}) async {
    if (advertise && !canAdvertise) throw PlatformException(code: 'no_advertiser');
    started = true;
  }
  @override
  Future<void> stop() async => started = false;
  @override
  Stream<BleEvent> get events => _events.stream;
  @override
  Future<void> reply(String conn, Uint8List data) async {}
  @override
  Future<Uint8List> exchange(String device, Uint8List message) async {
    if (dropping) throw PlatformException(code: 'ble', message: 'timeout');
    return Uint8List.fromList([...message, 0]);
  }

  @override
  Future<void> disconnect(String device) async {}
  @override
  Future<WifiBands> wifiBands() async => (create: 3, join: 3);
}

final _data = base64Encode([1, 2, 3]);

FakeCore _core(Map<String, Map<String, Object?>> steps) => FakeCore()
  ..on(
    'get_status',
    (_) => const FakeAnswer(
      replies: [
        CoreEvent('status', data: {'device_name': 'Galaxy A57', 'instance': 'medXfer-7810'}),
      ],
    ),
  )
  ..on('set_discoverable', (_) => const FakeAnswer())
  ..on(
    'scan',
    (_) => const FakeAnswer(
      replies: [
        CoreEvent(
          'peers_list',
          data: [
            {'device_name': 'medXfer-1a2b', 'host_ip': '192.168.1.30', 'port': 18887},
          ],
        ),
      ],
    ),
  )
  ..on(
    'ble_client',
    (p) => FakeAnswer(
      replies: [
        CoreEvent('ble_client', data: steps[p['step']] ?? {'data': _data}),
      ],
    ),
  );

Future<AppState> _app(WidgetTester tester, FakeCore core, _Radio radio) async {
  final state = AppState(core: core, folders: const _TestFolders(), ble: radio, history: MemoryHistoryStore());
  tester.platformDispatcher.localesTestValue = [const Locale('fr')];
  addTearDown(tester.platformDispatcher.clearLocalesTestValue);
  tester.view.physicalSize = const Size(1080, 2400);
  tester.view.devicePixelRatio = 2.75;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MedxferApp(state: state, settings: AppSettings()));
  await state.start();
  await tester.pumpAndSettle();
  return state;
}

Future<void> _reveal(WidgetTester tester, Finder finder) async {
  final list = find.descendant(of: find.byType(ConnectScreen), matching: find.byType(Scrollable)).first;
  await tester.scrollUntilVisible(finder, 200, scrollable: list);
  await tester.pumpAndSettle();
}

void main() {
  setUpAll(() {
    BleLink.recognizeJitter = Duration.zero;
    BleLink.touchRetryAfter = Duration.zero;
  });

  testWidgets('BLE-12: the reason first, the system\'s dialog only on "Autoriser"; a device found by Bluetooth, its PIN, a refusal said', (tester) async {
    final radio = _Radio()..allowed = false;
    final core = _core({
      'recognize': {'suffix': '6e2a', 'trusted': false},
      'pin_ready': {'ready': true},
      'pin_reply': {'refused': true},
    });
    await _app(tester, core, radio);
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();

    await _reveal(tester, find.byKey(const Key('ble-status')));
    expect(find.textContaining('Trouver les appareils proches par Bluetooth'), findsOneWidget);
    expect((radio.asked, radio.started), (0, false));
    await tester.tap(find.byKey(const Key('ble-action')));
    await tester.pumpAndSettle();
    expect((radio.asked, radio.started), (1, true));
    expect(find.byKey(const Key('ble-status')), findsNothing);

    radio.add(const BleSeen('11:11', -60));
    await tester.pumpAndSettle();
    await _reveal(tester, find.text('medXfer-6e2a'));
    expect(find.text('Bluetooth · Il affichera un PIN à taper ici'), findsOneWidget);

    await tester.tap(find.text('medXfer-6e2a'));
    await tester.pumpAndSettle();
    expect(find.text('Tapez le PIN affiché sur medXfer-6e2a.'), findsOneWidget);
    await tester.enterText(find.byKey(const Key('pin-field')), '054 461');
    await tester.pump();
    await tester.tap(find.byKey(const Key('pin-ok')));
    await tester.pumpAndSettle();
    expect(core.calls.firstWhere((c) => c.action == 'ble_client' && c.payload['step'] == 'pin').payload['pin'], '054 461');
    expect(find.text('medXfer-6e2a a refusé, ou le PIN était faux.'), findsOneWidget);
  });

  testWidgets('a device on the same network and found by Bluetooth: listed once, by the network', (tester) async {
    final radio = _Radio();
    final core = _core({
      'recognize': {'suffix': '1a2b', 'trusted': false},
    });
    await _app(tester, core, radio);
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();
    expect(radio.started, isTrue); // the screen opened: no dialog, the permission granted already
    radio.add(const BleSeen('22:22', -50));
    await tester.pumpAndSettle();
    await _reveal(tester, find.text('medXfer-1a2b'));
    expect(find.text('medXfer-1a2b'), findsOneWidget);
    expect(find.byKey(const ValueKey('ble-22:22')), findsNothing);
  });

  testWidgets('the edge of reach: the weak signal said in the row; the touch still dropped, "too far" said', (tester) async {
    final radio = _Radio();
    final core = _core({
      'recognize': {'suffix': '6e2a', 'trusted': false},
    });
    await _app(tester, core, radio);
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();
    radio.add(const BleSeen('11:11', -90));
    await tester.pumpAndSettle();
    await _reveal(tester, find.text('medXfer-6e2a'));
    expect(find.text('Bluetooth · signal faible · Il affichera un PIN à taper ici'), findsOneWidget);
    radio.dropping = true;
    await tester.tap(find.text('medXfer-6e2a'));
    await tester.pumpAndSettle();
    expect(core.calls.where((c) => c.action == 'ble_client' && c.payload['step'] == 'hello'), hasLength(1 + BleLink.touchTries));
    expect(find.text('medXfer-6e2a est trop loin pour le Bluetooth : rapprochez-vous, puis touchez-le à nouveau.'), findsOneWidget);
  });

  testWidgets('an adapter that cannot advertise: said, and the devices it sees still listed', (tester) async {
    final radio = _Radio()..canAdvertise = false;
    final core = _core({
      'recognize': {'suffix': '6e2a', 'trusted': false},
    });
    await _app(tester, core, radio);
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();
    await _reveal(tester, find.byKey(const Key('ble-status')));
    expect(find.textContaining("ne peut pas s'annoncer"), findsOneWidget);
    radio.add(const BleSeen('11:11', -50));
    await tester.pumpAndSettle();
    await _reveal(tester, find.text('medXfer-6e2a'));
    expect(find.text('medXfer-6e2a'), findsOneWidget);
  });
}
