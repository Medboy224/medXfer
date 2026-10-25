// WP-B.10 on screen: the receive folder in the settings, chosen with the system's picker (a
// desktop), then back to the default one.
import 'dart:io';

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
  @override
  Future<String> dataDir() async => '/data';
  @override
  Future<String> downloadDir() async => '/home/awa/Downloads/medXfer';
}

/// The system's folder picker, answering [dir].
class _Picker implements FilePicker {
  _Picker(this.dir);
  final String dir;
  @override
  bool get supported => true;
  @override
  bool get folders => true;
  @override
  Future<Picked> files() async => const Picked();
  @override
  Future<String?> folder() async => dir;
}

void main() {
  testWidgets('the receive folder: the default, then one chosen, then the default again', (tester) async {
    // Real disk access: outside the test's fake time.
    final chosen = (await tester.runAsync(() => Directory.systemTemp.createTemp('medxfer-recu')))!;
    addTearDown(() => chosen.deleteSync(recursive: true));
    final settings = AppSettings(store: MemorySettingsStore());
    final core = FakeCore()..on('get_status', (_) => const FakeAnswer(replies: [CoreEvent('status', data: {'device_name': 'PC'})]));
    final state = AppState(core: core, folders: _Folders(), filePicker: _Picker(chosen.path), history: MemoryHistoryStore(), settings: settings);
    tester.platformDispatcher.localesTestValue = [const Locale('fr')];
    addTearDown(tester.platformDispatcher.clearLocalesTestValue);
    await tester.pumpWidget(MedxferApp(state: state, settings: settings));
    await state.start();
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('home-menu')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Réglages'));
    await tester.pumpAndSettle();
    expect(find.text('Par défaut : /home/awa/Downloads/medXfer'), findsOneWidget);
    // A desktop: no sorting by type.
    expect(find.byKey(const Key('sort-by-type')), findsNothing);

    await tester.runAsync(() async {
      await tester.tap(find.byKey(const Key('receive-choose')));
      await Future<void>.delayed(const Duration(milliseconds: 200)); // the write test, on disk
    });
    await tester.pumpAndSettle();
    expect(settings.receiveDir, chosen.path);
    expect(find.text(chosen.path), findsOneWidget);
    expect(await tester.runAsync(state.receiveFolders.current), chosen.path);

    await tester.tap(find.byKey(const Key('receive-reset')));
    await tester.pumpAndSettle();
    expect(settings.receiveDir, '');
    expect(find.text('Par défaut : /home/awa/Downloads/medXfer'), findsOneWidget);
  });
}
