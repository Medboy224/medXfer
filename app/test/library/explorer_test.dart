// The explorer's image cache (WP-B.9): loaded once however many ask, then served from memory,
// the oldest dropped past the bound.
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'dart:async';

import 'package:medxfer/library/explorer.dart';
import 'package:medxfer/library/selection.dart';
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

  test('at most a few loads at once, the last asked first; a tile gone is not loaded', () async {
    final e = Explorer(const NoLibrary());
    final started = <String>[];
    final gates = <String, Completer<Uint8List?>>{};
    Future<Uint8List?> Function() load(String k) => () {
      started.add(k);
      return (gates[k] = Completer<Uint8List?>()).future;
    };
    for (var i = 0; i < Explorer.parallel + 3; i++) {
      e.image('k$i', load('k$i'));
    }
    // The first ones start at once, while there is room.
    expect(started, [for (var i = 0; i < Explorer.parallel; i++) 'k$i']);
    e.forget('k${Explorer.parallel}'); // scrolled away while waiting
    gates['k0']!.complete(Uint8List(1));
    await Future<void>.delayed(Duration.zero);
    await Future<void>.delayed(Duration.zero);
    // The last asked of those waiting goes next; the forgotten one never.
    expect(started.last, 'k${Explorer.parallel + 2}');
    expect(started, isNot(contains('k${Explorer.parallel}')));
  });

  test('a picked folder gets its size once walked', () async {
    final e = Explorer(_Sizes());
    final s = Selection()..toggle(const Pick(path: '/Docs', name: 'Docs', size: 0, kind: PickKind.folder));
    e.sizeFolders(s);
    await Future<void>.delayed(Duration.zero);
    expect(s.bytes, 42);
  });

  test('a category of files, in pages', () async {
    final e = Explorer(_Sizes());
    e.showCategory(FileCategory.documents);
    await Future<void>.delayed(Duration.zero);
    expect(e.files(FileCategory.documents).items.single.name, 'a.pdf');
    expect(e.picksOf(5).single.path, '/a.pdf');
    e.showCategory(null);
    expect(e.category, isNull);
  });
}

class _Sizes extends NoLibrary {
  const _Sizes();
  @override
  Future<int> dirSize(String path) async => 42;
  @override
  Future<List<FileEntry>> files(FileCategory category, {int offset = 0, int limit = 200}) async =>
      offset > 0 ? const [] : const [FileEntry(name: 'a.pdf', path: '/a.pdf', dir: false, size: 1)];
}
