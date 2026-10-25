// WP-B.5: the settings kept across launches, the developer mode (DEV-13) and its panel
// (DEV-19-R), on the fake core.
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/dev/dev_tools.dart';
import 'package:medxfer/features/dev/dev_panel.dart';
import 'package:medxfer/platform/folders.dart';
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

CoreEvent _progress({double speed = 40, int bytes = 1 << 20, int total = 10 << 20}) => CoreEvent(
  'transfer_progress',
  data: {
    'speed_mbps': speed,
    'batch_bytes': bytes,
    'batch_total_bytes': total,
    'stage_ms': {'net_wait': 120.0, 'disk_write': 480.5, 'fsync': 60.0},
    'bottleneck': 'disk_write',
    'bottleneck_confidence': 'high',
  },
);

Future<(FakeCore, AppState, AppSettings)> _app(WidgetTester tester, {bool devMode = false}) async {
  final core = FakeCore()
    ..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'Galaxy A57'})]));
  final state = AppState(core: core, folders: const _TestFolders(), history: MemoryHistoryStore());
  final settings = AppSettings(devMode: devMode, store: MemorySettingsStore());
  tester.platformDispatcher.localesTestValue = [const Locale('fr')];
  addTearDown(tester.platformDispatcher.clearLocalesTestValue);
  await tester.pumpWidget(MedxferApp(state: state, settings: settings));
  await state.start();
  await tester.pumpAndSettle();
  return (core, state, settings);
}

void main() {
  group('settings', () {
    test('kept in settings.json, read back at the next launch', () async {
      final dir = await Directory.systemTemp.createTemp('medxfer-settings');
      addTearDown(() => dir.delete(recursive: true));
      final store = FileSettingsStore(() async => dir.path);
      final first = AppSettings(store: store)
        ..theme = ThemeChoice.black
        ..accent = accents[3]
        ..devMode = true;
      await first.saved;
      final next = AppSettings(store: store);
      await next.load();
      expect((next.theme, next.accent, next.devMode), (ThemeChoice.black, accents[3], true));
    });

    test('a damaged file or unknown values keep the defaults', () async {
      final dir = await Directory.systemTemp.createTemp('medxfer-settings');
      addTearDown(() => dir.delete(recursive: true));
      await File('${dir.path}/settings.json').writeAsString('{"theme": "neon", "accent": 9, "dev_mode": "yes"}');
      final s = AppSettings(store: FileSettingsStore(() async => dir.path));
      await s.load();
      expect((s.theme, s.accent, s.devMode), (ThemeChoice.system, accents.first, false));
      await File('${dir.path}/settings.json').writeAsString('{not json');
      await s.load();
      expect(s.devMode, isFalse);
    });
  });

  group('DevTools', () {
    test('the live telemetry: speed, stages, limiting stage; a new transfer starts its own curve', () async {
      final core = FakeCore();
      final dev = DevTools(core, downloadDir: () async => '/out');
      addTearDown(dev.dispose);
      core.emit(_progress(speed: 30));
      core.emit(_progress(speed: 45, bytes: 2 << 20));
      await pumpEventQueue();
      expect(dev.speeds, [30, 45]);
      expect(dev.live!.stages['disk_write'], 480.5);
      expect((dev.live!.bottleneck, dev.live!.confidence), ('disk_write', 'high'));
      core.emit(_progress(speed: 10, total: 99));
      await pumpEventQueue();
      expect(dev.speeds, [10]);
    });

    test('a benchmark answers later, with the id of its call', () async {
      final core = FakeCore()..on('test_disk', (_) => const FakeAnswer());
      final dev = DevTools(core, downloadDir: () async => '/out');
      addTearDown(dev.dispose);
      final run = dev.runDisk();
      await pumpEventQueue();
      expect(dev.running(DevJob.disk), isTrue);
      expect(core.calls.single.payload, {'dir': '/out', 'size_bytes': DevTools.diskBenchBytes});
      core.emit(const CoreEvent('benchmark_disk_result', id: 'ui-0', data: {'write_speed_mbps': 1.0})); // another call's
      core.emit(const CoreEvent('benchmark_disk_result', id: 'ui-1', data: {'write_speed_mbps': 180.5, 'read_speed_mbps': 900.0, 'rating': 'fast'}));
      await run;
      expect(dev.running(DevJob.disk), isFalse);
      expect((dev.disk!.writeMBps, dev.disk!.readMBps), (180.5, 900.0));
    });

    test("the core's refusal is the job's error", () async {
      final core = FakeCore()
        ..on('test_network', (_) => const FakeAnswer(ok: false, error: 'Not paired with any device.'))
        ..on('export_bundle', (_) => const FakeAnswer(replies: [CoreEvent('action_error', data: {'error': 'disk full'})]));
      final dev = DevTools(core, downloadDir: () async => '/out');
      addTearDown(dev.dispose);
      await dev.runNetwork();
      expect(dev.error(DevJob.network), 'Not paired with any device.');
      await dev.exportBundle();
      expect(dev.error(DevJob.bundle), 'disk full');
      expect(dev.bundle, isNull);
    });

    test('the report and the bundle, from the replies of the call', () async {
      final core = FakeCore()
        ..on('get_transfer_summary', (_) => const FakeAnswer(replies: [CoreEvent('last_transfer_summary', data: {'formatted_report': 'Speed: 52 MB/s'})]))
        ..on('export_bundle', (_) => const FakeAnswer(replies: [CoreEvent('bundle_exported', data: {'path': '/out/medxfer-bundle-1.zip'})]));
      final dev = DevTools(core, downloadDir: () async => '/out');
      addTearDown(dev.dispose);
      await dev.loadReport();
      await dev.exportBundle();
      expect((dev.report, dev.bundle), ('Speed: 52 MB/s', '/out/medxfer-bundle-1.zip'));
    });
  });

  testWidgets('developer mode off: no banner, no panel in the menu', (tester) async {
    await _app(tester);
    expect(find.byKey(const Key('dev-banner')), findsNothing);
    await tester.tap(find.byKey(const Key('home-menu')));
    await tester.pumpAndSettle();
    expect(find.text('Panneau développeur'), findsNothing);
  });

  testWidgets('switched on in the settings: the banner, then the panel with the live transfer', (tester) async {
    final (core, _, settings) = await _app(tester);
    await tester.tap(find.byKey(const Key('home-menu')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Réglages'));
    await tester.pumpAndSettle();
    // Below the receive folder: further down the list.
    await tester.ensureVisible(find.byKey(const Key('dev-mode')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('dev-mode')));
    await tester.pumpAndSettle();
    expect(settings.devMode, isTrue);
    expect(find.text('Mode développeur · ouvrir le panneau'), findsOneWidget);

    core.emit(_progress(speed: 52));
    await tester.tap(find.byKey(const Key('dev-banner')));
    await tester.pumpAndSettle();
    expect(find.text('Panneau développeur'), findsOneWidget);
    expect(find.byKey(const Key('live-speed')), findsOneWidget);
    expect(find.text('Étape limitante : écriture disque (nette)'), findsOneWidget);
    // No device: the network benchmark says to connect one, and cannot run.
    expect(find.text("Connectez d'abord un appareil."), findsOneWidget);
    // The banner again, over the panel: still one panel.
    await tester.tap(find.byKey(const Key('dev-banner')));
    await tester.pumpAndSettle();
    expect(find.byType(DevPanel, skipOffstage: false), findsOneWidget);
  });
}
