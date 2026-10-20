/// The real core: the C library of cmd/libmedxfer, through dart:ffi (API-05, gate G2a).
///
/// - Every call runs in a worker isolate: an action that takes a while (a network scan) never
///   holds the interface.
/// - The events are read by a background isolate that waits in medxfer_next_event without
///   limit, then takes what else is queued, 256 at most, and sends the batch (B10). It stops
///   on the core's sentinel (B9): never killed inside a native call.
/// - No Dart pointer is given to the core (B8); every string the library returns is freed
///   with medxfer_free (B1).
library;

import 'dart:async';
import 'dart:convert';
import 'dart:ffi';
import 'dart:io';
import 'dart:isolate';

import 'package:flutter/foundation.dart';

import 'package:ffi/ffi.dart';

import 'core.dart';

/// The library's file name on this system.
String coreLibraryName() => Platform.isWindows ? 'medxfer.dll' : 'libmedxfer.so';

typedef _StrC = Pointer<Utf8> Function(Pointer<Utf8>);
typedef _NextC = Pointer<Utf8> Function(Int32);
typedef _NextDart = Pointer<Utf8> Function(int);
typedef _VoidC = Void Function();
typedef _VoidDart = void Function();
typedef _FreeC = Void Function(Pointer<Utf8>);
typedef _FreeDart = void Function(Pointer<Utf8>);

/// The five functions of the library (bible §10.4).
class _Lib {
  _Lib(String path) : this._(DynamicLibrary.open(path));

  _Lib._(DynamicLibrary lib)
      : start = lib.lookupFunction<_StrC, _StrC>('medxfer_start'),
        call = lib.lookupFunction<_StrC, _StrC>('medxfer_call'),
        next = lib.lookupFunction<_NextC, _NextDart>('medxfer_next_event'),
        stop = lib.lookupFunction<_VoidC, _VoidDart>('medxfer_stop'),
        free = lib.lookupFunction<_FreeC, _FreeDart>('medxfer_free');

  final _StrC start;
  final _StrC call;
  final _NextDart next;
  final _VoidDart stop;
  final _FreeDart free;

  /// Calls fn with arg; Dart frees its own string, the library frees its own (B1).
  String roundTrip(_StrC fn, String arg) {
    final p = arg.toNativeUtf8();
    try {
      final r = fn(p);
      final s = r.toDartString();
      free(r);
      return s;
    } finally {
      calloc.free(p);
    }
  }
}

/// The real core.
class FfiCore implements Core {
  FfiCore({String? libraryPath}) : _path = libraryPath ?? coreLibraryName();

  final String _path;
  final _events = StreamController<CoreEvent>.broadcast();
  int _nextId = 0;
  Isolate? _reader;

  @override
  Stream<CoreEvent> get events => _events.stream;

  @override
  Future<CoreStart> start(CoreConfig config) async {
    final path = _path;
    final json = jsonEncode(config.toJson());
    final out = await Isolate.run(() {
      final lib = _Lib(path);
      return lib.roundTrip(lib.start, json);
    });
    final started = CoreStart.fromJson(jsonDecode(out) as Map<String, Object?>);
    if (started.ok) {
      await _startReader();
    }
    return started;
  }

  @override
  Future<CoreReply> call(String action, [Map<String, Object?>? payload]) async {
    final id = 'ui-${++_nextId}';
    final request = jsonEncode({'id': id, 'action': action, 'payload': ?payload});
    final path = _path;
    final out = await Isolate.run(() {
      final lib = _Lib(path);
      return lib.roundTrip(lib.call, request);
    });
    return CoreReply.fromJson(jsonDecode(out) as Map<String, Object?>);
  }

  @override
  Future<void> stop() async {
    final path = _path;
    // The core wakes the reader with its sentinel (B9); the reader then leaves by itself.
    await Isolate.run(() => _Lib(path).stop());
  }

  Future<void> _startReader() async {
    if (_reader != null) return; // a hot restart: the reader runs already
    final port = ReceivePort();
    port.listen((msg) {
      if (msg is! List) return;
      for (final s in msg) {
        final e = CoreEvent.fromJson(jsonDecode(s as String) as Map<String, Object?>);
        // Debug builds: each event's name in the log (logcat), the core's own log lines whole.
        // No other data: it may name files or hold a PIN.
        if (kDebugMode) debugPrint(e.name == 'log' ? 'core log: ${e.data}' : 'core event: ${e.name}${e.id == null ? '' : ' (${e.id})'}');
        _events.add(e);
        if (e.name == CoreEvent.stopped) {
          port.close();
          _reader = null;
        }
      }
    });
    _reader = await Isolate.spawn(_readEvents, [port.sendPort, _path]);
  }

  /// The reader isolate: one blocking wait, then the rest of the queue, sent as one batch.
  static void _readEvents(List<Object> args) {
    final out = args[0] as SendPort;
    final lib = _Lib(args[1] as String);
    String? take(int timeoutMs) {
      final p = lib.next(timeoutMs);
      if (p == nullptr) return null;
      final s = p.toDartString();
      lib.free(p);
      return s;
    }

    while (true) {
      final first = take(-1); // no limit: the core wakes us, at the latest on stop (B9)
      if (first == null) continue;
      final batch = <String>[first];
      var stopped = first.contains('"${CoreEvent.stopped}"');
      while (!stopped && batch.length < 256) {
        final s = take(0);
        if (s == null) break;
        batch.add(s);
        stopped = s.contains('"${CoreEvent.stopped}"');
      }
      out.send(batch);
      if (stopped) return;
    }
  }
}
