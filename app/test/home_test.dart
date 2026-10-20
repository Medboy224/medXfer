// The home screen of the mockups, on the fake core: the tabs, the Connect button, and the
// core's line, which says the device's name once the core has started.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
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

Future<(FakeCore, AppState)> _app(WidgetTester tester, {Locale locale = const Locale('fr'), AppSettings? settings}) async {
  final core = FakeCore()
    ..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'Galaxy A57'})]));
  final state = AppState(core: core, folders: const _TestFolders(), history: MemoryHistoryStore());
  tester.platformDispatcher.localesTestValue = [locale];
  addTearDown(tester.platformDispatcher.clearLocalesTestValue);
  await tester.pumpWidget(MedxferApp(state: state, settings: settings ?? AppSettings()));
  await state.start();
  await tester.pumpAndSettle();
  return (core, state);
}

void main() {
  testWidgets('the home screen: tabs, Connect, the core ready under its name', (tester) async {
    final (core, state) = await _app(tester);
    expect(state.status, CoreStatus.ready);
    for (final tab in ['Historique', 'Apps', 'Photos', 'Vidéos', 'Musique', 'Fichiers']) {
      expect(find.text(tab), findsOneWidget, reason: tab);
    }
    expect(find.text('Connecter'), findsOneWidget);
    expect(find.text('Sur cet appareil'), findsOneWidget);
    expect(find.text('Prêt · Galaxy A57'), findsOneWidget);
    expect(core.calls.map((c) => c.action), ['get_status']);
  });

  testWidgets('the bottom bar keeps to its button: the tabs stay visible', (tester) async {
    await _app(tester);
    final bar = tester.getSize(find.byKey(const Key('bottom-bar')));
    expect(bar.height, lessThan(120), reason: 'the bottom bar covers the screen');
    expect(tester.getRect(find.text('Apps')).top, lessThan(tester.getRect(find.byKey(const Key('bottom-bar'))).top));
  });

  testWidgets('English, by the system language', (tester) async {
    await _app(tester, locale: const Locale('en'));
    expect(find.text('Connect'), findsOneWidget);
    expect(find.text('Videos'), findsOneWidget);
  });

  testWidgets('a core of another schema is refused, and said', (tester) async {
    final core = _OldCore();
    final state = AppState(core: core, folders: const _TestFolders(), history: MemoryHistoryStore());
    await tester.pumpWidget(MedxferApp(state: state, settings: AppSettings()));
    await state.start();
    await tester.pumpAndSettle();
    expect(state.status, CoreStatus.failed);
    expect(state.error, contains('schema 3'));
  });

  testWidgets('the theme and accent follow the settings (UI-4)', (tester) async {
    final settings = AppSettings(theme: ThemeChoice.black, accent: accents[2]);
    await _app(tester, settings: settings);
    final ctx = tester.element(find.byType(Scaffold));
    expect(Theme.of(ctx).colorScheme.primary, accents[2]);
    expect(Theme.of(ctx).scaffoldBackgroundColor, MedxferColors.black.background);
    settings.theme = ThemeChoice.light;
    await tester.pumpAndSettle();
    expect(Theme.of(tester.element(find.byType(Scaffold))).scaffoldBackgroundColor, MedxferColors.light.background);
  });
}

/// A core that says it speaks schema 3.
class _OldCore extends FakeCore {
  @override
  Future<CoreStart> start(CoreConfig config) async => const CoreStart(ok: true, schema: 3);
}
