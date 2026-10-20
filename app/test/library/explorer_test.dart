// The explorer's image cache (WP-B.9): loaded once however many ask, then served from memory,
// the oldest dropped past the bound.
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/library/explorer.dart';
import 'package:medxfer/platform/library.dart';

void main() {
  test('an image is loaded once, its future completes, then the cache serves it', () async {
    final e = Explorer(const NoLibrary());
    var loads = 0;
    Future<Uint8List?> load() async {
      loads++;
      return Uint8List.fromList([1, 2, 3]);
    }

    final a = e.image('images:1', load);
    final b = e.image('images:1', load);
    // Both callers get the bytes: the future must complete (it once waited for itself).
    expect(await a.timeout(const Duration(seconds: 1)), [1, 2, 3]);
    expect(await b.timeout(const Duration(seconds: 1)), [1, 2, 3]);
    expect(loads, 1);
    expect(e.cached('images:1'), [1, 2, 3]);
    expect(await e.image('images:1', load), [1, 2, 3]);
    expect(loads, 1);
  });

  test('past the bound, the least recently used image goes', () async {
    final e = Explorer(const NoLibrary());
    for (var i = 0; i < Explorer.cacheSize + 1; i++) {
      await e.image('k$i', () async => Uint8List(1));
    }
    expect(e.cached('k0'), isNull);
    expect(e.cached('k${Explorer.cacheSize}'), isNotNull);
  });

  test('a failed load gives null, and is tried again later', () async {
    final e = Explorer(const NoLibrary());
    expect(await e.image('x', () async => throw StateError('gone')), isNull);
    expect(await e.image('x', () async => Uint8List(2)), hasLength(2));
  });
}
