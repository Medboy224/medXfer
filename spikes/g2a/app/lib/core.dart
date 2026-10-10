// FFI bindings to the Go core (plan §4.3) and the two event models of G2A-03:
// "callback" (NativeCallable.listener, Go calls a function pointer from its own thread) and
// "poll" (a background isolate blocks in medxfer_next_event and forwards events).
import 'dart:async';
import 'dart:convert';
import 'dart:ffi';
import 'dart:io';
import 'dart:isolate';

import 'package:ffi/ffi.dart';

String libraryName() => Platform.isWindows ? 'medxfer.dll' : 'libmedxfer.so';

typedef _StrFnC = Pointer<Utf8> Function(Pointer<Utf8>);
typedef _EventCbC = Void Function(Pointer<Utf8>);
typedef _SetCbC = Void Function(Pointer<NativeFunction<_EventCbC>>);
typedef _SetCbDart = void Function(Pointer<NativeFunction<_EventCbC>>);
typedef _NextC = Pointer<Utf8> Function(Int32);
typedef _NextDart = Pointer<Utf8> Function(int);
typedef _VoidC = Void Function();
typedef _VoidDart = void Function();
typedef _FreeC = Void Function(Pointer<Utf8>);
typedef _FreeDart = void Function(Pointer<Utf8>);

class Core {
  Core._(this.lib)
      : _start = lib.lookupFunction<_StrFnC, _StrFnC>('medxfer_start'),
        _call = lib.lookupFunction<_StrFnC, _StrFnC>('medxfer_call'),
        _setCb = lib.lookupFunction<_SetCbC, _SetCbDart>('medxfer_set_event_callback'),
        _stop = lib.lookupFunction<_VoidC, _VoidDart>('medxfer_stop'),
        free = lib.lookupFunction<_FreeC, _FreeDart>('medxfer_free');

  final DynamicLibrary lib;
  final _StrFnC _start;
  final _StrFnC _call;
  final _SetCbDart _setCb;
  final _VoidDart _stop;
  final _FreeDart free;

  static Core open() => Core._(DynamicLibrary.open(libraryName()));

  // B1: Dart frees what Dart allocated (calloc), Go frees what Go allocated (medxfer_free).
  String _roundTrip(_StrFnC fn, String arg) {
    final p = arg.toNativeUtf8();
    final r = fn(p);
    calloc.free(p);
    final s = r.toDartString();
    free(r);
    return s;
  }

  Map<String, dynamic> start(Map<String, dynamic> config) =>
      jsonDecode(_roundTrip(_start, jsonEncode(config))) as Map<String, dynamic>;

  int _nextId = 0;
  Map<String, dynamic> call(String action, [Map<String, dynamic>? payload]) {
    final req = {'id': 'r${++_nextId}', 'action': action, if (payload != null) 'payload': payload};
    return jsonDecode(_roundTrip(_call, jsonEncode(req))) as Map<String, dynamic>;
  }

  /// Raw round trip, for the latency loop: no JSON decoding on the Dart side.
  String callRaw(String requestJson) => _roundTrip(_call, requestJson);

  void stop() => _stop();
  void setCallback(Pointer<NativeFunction<_EventCbC>> cb) => _setCb(cb);
}

/// Source of core events, decoded as JSON maps.
abstract class EventSource {
  Stream<Map<String, dynamic>> get events;
  String get name;
  Future<void> start();
  Future<void> stop();
}

/// Callback model: Go calls the native function created by NativeCallable.listener, from any
/// thread; Dart receives the call asynchronously on this isolate and frees the string (B1).
class CallbackSource implements EventSource {
  CallbackSource(this.core);
  final Core core;
  final _ctl = StreamController<Map<String, dynamic>>.broadcast(sync: true);
  NativeCallable<_EventCbC>? _callable;

  @override
  String get name => 'callback';
  @override
  Stream<Map<String, dynamic>> get events => _ctl.stream;

  @override
  Future<void> start() async {
    _callable = NativeCallable<_EventCbC>.listener((Pointer<Utf8> p) {
      final s = p.toDartString();
      core.free(p);
      _ctl.add(jsonDecode(s) as Map<String, dynamic>);
    });
    core.setCallback(_callable!.nativeFunction);
  }

  @override
  Future<void> stop() async {
    core.setCallback(nullptr);
    // Let calls already queued by Go reach Dart before closing the callable.
    await Future<void>.delayed(const Duration(milliseconds: 200));
    _callable?.close();
    _callable = null;
  }
}

/// Poll model: a background isolate loops on medxfer_next_event(500 ms) and posts each event
/// to this isolate; the blocking call never touches the UI isolate.
class PollSource implements EventSource {
  PollSource(this.core);
  final Core core;
  final _ctl = StreamController<Map<String, dynamic>>.broadcast(sync: true);
  Isolate? _isolate;
  ReceivePort? _port;
  ReceivePort? _exit;

  @override
  String get name => 'poll';
  @override
  Stream<Map<String, dynamic>> get events => _ctl.stream;

  @override
  Future<void> start() async {
    _port = ReceivePort();
    _port!.listen((msg) {
      for (final s in msg as List) {
        _ctl.add(jsonDecode(s as String) as Map<String, dynamic>);
      }
    });
    _exit = ReceivePort();
    _isolate = await Isolate.spawn(_pollLoop, [_port!.sendPort, libraryName()], onExit: _exit!.sendPort);
  }

  static void _pollLoop(List<Object> args) {
    final out = args[0] as SendPort;
    final lib = DynamicLibrary.open(args[1] as String);
    final next = lib.lookupFunction<_NextC, _NextDart>('medxfer_next_event');
    final free = lib.lookupFunction<_FreeC, _FreeDart>('medxfer_free');
    while (true) {
      // Batch: after one event, take whatever else is already queued (up to 256), so a burst
      // costs one inter-isolate message per batch instead of one per event.
      final batch = <String>[];
      var p = next(500);
      while (p != nullptr) {
        final s = p.toDartString();
        free(p);
        if (s.contains('"__poll_close__"')) {
          if (batch.isNotEmpty) out.send(batch);
          return; // orderly stop: the isolate exits
        }
        batch.add(s);
        p = batch.length < 256 ? next(0) : nullptr;
      }
      if (batch.isNotEmpty) out.send(batch);
    }
  }

  @override
  Future<void> stop() async {
    // Coordinated stop: the core wakes the reader, which returns; only then is the isolate
    // gone. Isolate.kill alone left a reader blocked in medxfer_next_event that stole the
    // next test's first event.
    core.call('poll_close');
    await _exit?.first.timeout(const Duration(seconds: 2), onTimeout: () {
      _isolate?.kill(priority: Isolate.immediate);
      return null;
    });
    _isolate = null;
    _exit?.close();
    _port?.close();
    _exit = _port = null;
  }
}
