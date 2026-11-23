// The grids' geometry (plan J8, WP-8.1b): square tiles, and the mosaic agreed on 23 November
// 2026: rows of photos at one height each, in their own proportions, as wide as the screen.
import 'dart:ui';

import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/features/explorer/grid_layout.dart';

const _width = 400.0, _gap = 2.0;

GridLayout _mosaic(List<double> aspects, {List<int>? groups, bool headers = false}) =>
    GridLayout.mosaic(groups: groups ?? [aspects.length], headers: headers, width: _width, aspects: aspects, gap: _gap, header: 52);

/// The rows of a mosaic, as lists of photo indices.
List<List<int>> _rows(GridLayout l) => [
  for (final group in l.groupRows)
    for (final r in group) [for (var i = r.first; i < r.first + r.count; i++) i],
];

void main() {
  test('square tiles: 3 to a row, the next day under its header', () {
    final l = GridLayout.squares(groups: [4, 2], headers: true, width: _width, columns: 3, gap: _gap, header: 52);
    final tile = (_width - 2 * _gap) / 3;
    expect(l.rectOf(0), Rect.fromLTWH(0, 52, tile, tile));
    expect(l.rectOf(2).left, closeTo(2 * (tile + _gap), 1e-9));
    expect(l.rectOf(3).top, closeTo(52 + tile + _gap, 1e-9)); // second row
    expect(l.headerTops, [0, closeTo(52 + 2 * tile + _gap, 1e-9)]);
    expect(l.rectOf(4).top, closeTo(l.headerTops[1] + 52, 1e-9));
    expect(l.at(l.rectOf(4).top + 5, x: l.rectOf(4).left + 1), 4);
    expect(l.at(l.rectOf(5).top + 5, x: l.rectOf(5).left + 1), 5);
  });

  test('the mosaic: three tall photos (9:16) fill a row, at one height, each in its proportions', () {
    const tall = 9 / 16;
    final l = _mosaic([tall, tall, tall, tall, tall, tall]);
    expect(_rows(l), [
      [0, 1, 2],
      [3, 4, 5],
    ]);
    final a = l.rectOf(0), b = l.rectOf(1), c = l.rectOf(2);
    expect(b.height, a.height);
    expect(a.width / a.height, closeTo(tall, 0.01));
    expect(c.right, closeTo(_width, 1e-9), reason: 'as wide as the screen');
    expect(a.height, inInclusiveRange(_width * 0.45, _width * GridLayout.maxRowShare));
    expect(l.rectOf(3).top, closeTo(a.bottom + _gap, 1e-9));

    // A camera's photos (3:4) go two to a row: three would be far lower than aimed.
    expect(_rows(_mosaic([0.75, 0.75, 0.75, 0.75])), [
      [0, 1],
      [2, 3],
    ]);
  });

  test('the mosaic: a panorama has a lower row of its own; a landscape photo fills one alone', () {
    final pano = _mosaic([3, 0.75, 0.75, 0.75]);
    expect(_rows(pano).first, [0]);
    expect(pano.rectOf(0).width, closeTo(_width, 1e-9));
    expect(pano.rectOf(0).height, closeTo(_width / 3, 1e-9));

    final mixed = _mosaic([16 / 9, 0.75, 16 / 9, 0.75]);
    expect(_rows(mixed).first, [0]);
    expect(mixed.rectOf(0).width, closeTo(_width, 1e-9));
    expect(mixed.rectOf(0).height, closeTo(_width * 9 / 16, 1e-9));
    // A tall one and a wide one share the next row, at one height.
    expect(_rows(mixed)[1], [1, 2]);
    expect(mixed.rectOf(2).width, greaterThan(mixed.rectOf(1).width * 2));
    expect(mixed.rectOf(2).right, closeTo(_width, 1e-9));
  });

  test("the mosaic: a day's last photos keep the aimed height, to the left; the next day starts a row", () {
    const tall = 9 / 16;
    final l = _mosaic([tall, tall, tall, tall, 1], groups: [4, 1], headers: true);
    final rows = _rows(l);
    expect(rows, [
      [0, 1, 2],
      [3],
      [4],
    ]);
    final last = l.rectOf(3);
    expect(last.height, closeTo(_width * GridLayout.rowShare, 1e-9), reason: 'not stretched to the width');
    expect(last.left, 0);
    expect(last.right, lessThan(_width / 2));
    expect(l.headerTops, hasLength(2));
    expect(l.rectOf(4).top, closeTo(l.headerTops[1] + 52, 1e-9));
  });

  test("real test of 23 November: a day's last three tall photos fill the row, no gap on the right", () {
    final l = _mosaic([0.5, 0.5, 0.5, 1], groups: [3, 1]);
    expect(_rows(l).first, [0, 1, 2]);
    expect(l.rectOf(2).right, closeTo(_width, 1e-9));
    expect(l.rectOf(0).height, lessThanOrEqualTo(_width * GridLayout.maxRowShare));
  });

  test('the mosaic: proportions unknown show squares; extremes are cropped to 1:2 and 3:1', () {
    final unknown = _mosaic([0, 0, 0, 0]);
    expect(unknown.rectOf(0).width, closeTo(unknown.rectOf(0).height, 1e-9));
    final screenshots = _mosaic([9 / 19.5, 9 / 19.5, 9 / 19.5, 9 / 19.5, 9 / 19.5, 9 / 19.5]);
    final r = screenshots.rectOf(0);
    expect(r.width / r.height, closeTo(GridLayout.minAspect, 0.02));
  });

  test('the photo under a point, and those in view', () {
    final l = _mosaic([0.75, 1.5, 0.75, 0.75, 0.75, 0.75, 1, 1]);
    for (var i = 0; i < l.count; i++) {
      final r = l.rectOf(i);
      expect(l.at(r.center.dy, x: r.center.dx), i, reason: 'photo $i');
    }
    final seen = <int>[];
    l.visit(l.rectOf(3).top + 1, l.rectOf(3).bottom - 1, (i, _) => seen.add(i));
    expect(seen, containsAll(_rows(l)[1]));
    expect(seen, isNot(contains(0)));
  });
}
