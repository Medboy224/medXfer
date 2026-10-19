// The real core through dart:ffi (B1, B4, B9, B10): started, called, its events read in
// batches by the reader isolate, then stopped, the reader woken by the sentinel. Needs the
// library: MEDXFER_LIB, else ../build/lib/<system>/ (scripts/build-lib.*); skipped without it.
import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/ffi_core.dart';

String? libraryPath() {
  final env = Platform.environment['MEDXFER_LIB'];
  if (env != null && File(env).existsSync()) return env;
  final dir = Platform.isWindows ? 'windows-amd64' : 'linux-amd64';
  final f = File('../build/lib/$dir/${coreLibraryName()}');
  return f.existsSync() ? f.absolute.path : null;
}

void main() {
  final path = libraryPath();
  test('the real core: start, call, events, stop', () async {
    final core = FfiCore(libraryPath: path);
    final tmp = await Directory.systemTemp.createTemp('medxfer-ffi-');
    addTearDown(() => tmp.delete(recursive: true));
    final events = <CoreEvent>[];
    final stopped = Completer<void>();
    final sub = core.events.listen((e) {
      events.add(e);
      if (e.name == CoreEvent.stopped && !stopped.isCompleted) stopped.complete();
    });

    final started = await core.start(CoreConfig(dataDir: '${tmp.path}/data', downloadDir: '${tmp.path}/out', deviceName: 'ffi-test'));
    expect(started.ok, isTrue, reason: started.error);
    expect(started.schema, coreSchema);
    final again = await core.start(CoreConfig(dataDir: '${tmp.path}/data', downloadDir: '${tmp.path}/out'));
    expect(again.alreadyStarted, isTrue, reason: 'B4');

    final reply = await core.call('get_status');
    expect(reply.ok, isTrue, reason: reply.error);
    expect(reply.first('status')?.map['device_name'], 'ffi-test');
    final bad = await core.call('no_such_action');
    expect(bad.ok, isFalse);

    await core.stop();
    await stopped.future.timeout(const Duration(seconds: 10));
    await sub.cancel();
  }, skip: path == null ? 'no library: scripts/build-lib.* builds it' : null);
}
