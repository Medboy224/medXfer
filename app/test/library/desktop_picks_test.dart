// A desktop's files and folders together (WP-B.8): what it picks becomes the basket's picks,
// a folder sized once walked, and everything goes by path.
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/library/desktop_picks.dart';
import 'package:medxfer/library/selection.dart';
import 'package:medxfer/platform/library.dart';
import 'package:path/path.dart' as p;

void main() {
  late Directory dir;
  setUp(() => dir = Directory.systemTemp.createTempSync('medxfer-picks'));
  tearDown(() => dir.deleteSync(recursive: true));

  test('files and folders picked together, by path', () async {
    final file = File(p.join(dir.path, 'rapport.pdf'))..writeAsBytesSync([1, 2, 3]);
    final folder = Directory(p.join(dir.path, 'Photos'))..createSync();
    File(p.join(folder.path, 'a.jpg')).writeAsBytesSync(List.filled(5, 0));
    Directory(p.join(folder.path, 'sub')).createSync();
    File(p.join(folder.path, 'sub', 'b.jpg')).writeAsBytesSync(List.filled(7, 0));

    final picks = picksOfPaths([file.path, folder.path]);
    expect([for (final x in picks) (x.name, x.kind, x.size)], [('rapport.pdf', PickKind.file, 3), ('Photos', PickKind.folder, 0)]);
    expect(await const NoLibrary().dirSize(folder.path), 12);

    final s = Selection()..addAll(picks);
    s.addAll(picksOfPaths([file.path])); // picked again: still picked, not unpicked
    expect(s.count, 2);
    expect((s.plan(byPath: true) as ByPath).paths, [file.path, folder.path]);
  });
}
