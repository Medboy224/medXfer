/// Where each photo of a grid lies (plan J8, WP-8.1b): in square tiles of 3 to 5 columns, or in
/// a mosaic, rows of photos at the same height each, every photo in its own proportions, every
/// row as wide as the screen (as a phone's gallery does at its largest level; described and
/// agreed on 23 November 2026). The real grid, the painting of a pinch and a change of layout
/// all place the photos from it, so that they agree to the pixel.
library;

import 'dart:math' as math;
import 'dart:typed_data';
import 'dart:ui';

/// A row of the mosaic: its photos, from [first] (in the whole list), and where it stands.
class MosaicRow {
  const MosaicRow(this.first, this.count, this.top, this.height);

  final int first;
  final int count;
  final double top;
  final double height;
}

class GridLayout {
  GridLayout._(this.count, this._rects, this.headerTops, this.groupRows, this.extent);

  /// The proportions a photo keeps, width over height: beyond, it is cropped as a tile is (a
  /// screenshot, 9:19.5, shows at 1:2; a panorama at 3:1).
  static const minAspect = 0.5, maxAspect = 3.0;

  /// A mosaic row's height, a share of the width: aimed at, and at most when one photo less
  /// would leave it taller.
  static const rowShare = 0.6, maxRowShare = 0.8;

  /// Square tiles, [columns] to a row; each group (a day) under its header, if [headers].
  factory GridLayout.squares({required List<int> groups, required bool headers, required double width, required int columns, double gap = 2, double header = 52}) {
    final count = groups.fold(0, (a, b) => a + b);
    final rects = Float64List(count * 4);
    final stride = (width - (columns - 1) * gap) / columns + gap;
    final tile = stride - gap;
    final heads = <double>[];
    var y = 0.0;
    var i = 0;
    for (final n in groups) {
      if (headers) {
        heads.add(y);
        y += header;
      }
      for (var k = 0; k < n; k++, i++) {
        rects[4 * i] = (k % columns) * stride;
        rects[4 * i + 1] = y + (k ~/ columns) * stride;
        rects[4 * i + 2] = tile;
        rects[4 * i + 3] = tile;
      }
      final rows = (n + columns - 1) ~/ columns;
      if (rows > 0) y += rows * stride - gap;
    }
    return GridLayout._(count, rects, heads, const [], y);
  }

  /// The mosaic: [aspects] the photos' width over height (0 when unknown: square).
  ///
  /// A row takes the photos in their order while, at the aimed height (the width ×
  /// [rowShare]), they are not as wide as the screen; then its height is set for them to fill
  /// the width exactly, with or without the photo that passed it, whichever is nearer the aimed
  /// height. A day's last row, short of photos, keeps the aimed height and stays to the left.
  factory GridLayout.mosaic({required List<int> groups, required bool headers, required double width, required List<double> aspects, double gap = 2, double header = 52}) {
    final count = groups.fold(0, (a, b) => a + b);
    final rects = Float64List(count * 4);
    final heads = <double>[];
    final groupRows = <List<MosaicRow>>[];
    final aimed = width * rowShare, highest = width * maxRowShare;
    double aspect(int i) {
      final a = i < aspects.length ? aspects[i] : 0.0;
      return a.isFinite && a > 0 ? a.clamp(minAspect, maxAspect).toDouble() : 1.0;
    }

    // The height at which the photos from..to (excluded), [sum] of their aspects, fill the width.
    double filling(int from, int to, double sum) => (width - gap * (to - from - 1)) / sum;
    var y = 0.0;
    var start = 0;
    for (final n in groups) {
      if (headers) {
        heads.add(y);
        y += header;
      }
      final rows = <MosaicRow>[];
      final end = start + n;
      var i = start;
      while (i < end) {
        var j = i;
        var sum = 0.0;
        var height = 0.0;
        var filled = true;
        while (true) {
          sum += aspect(j);
          j++;
          final h = filling(i, j, sum);
          if (h <= aimed) {
            if (j - i > 1) {
              final without = filling(i, j - 1, sum - aspect(j - 1));
              if (without <= highest && math.log(without / aimed).abs() < math.log(aimed / h).abs()) {
                j--;
                sum -= aspect(j);
                height = without;
                break;
              }
            }
            height = h;
            break;
          }
          if (j == end) {
            height = aimed; // the day's last photos: not stretched to the width
            filled = false;
            break;
          }
        }
        var x = 0.0;
        for (var k = i; k < j; k++) {
          // A filled row's last photo takes what is left: no gap at the edge for a rounding.
          final w = filled && k == j - 1 ? width - x : aspect(k) * height;
          rects[4 * k] = x;
          rects[4 * k + 1] = y;
          rects[4 * k + 2] = w;
          rects[4 * k + 3] = height;
          x += w + gap;
        }
        rows.add(MosaicRow(i, j - i, y, height));
        y += height + gap;
        i = j;
      }
      groupRows.add(rows);
      start = end;
    }
    return GridLayout._(count, rects, heads, groupRows, y);
  }

  final int count;
  final Float64List _rects; // left, top, width, height of each photo
  /// The top of each group's header, when there are headers.
  final List<double> headerTops;

  /// The mosaic's rows, group by group; empty for square tiles.
  final List<List<MosaicRow>> groupRows;

  /// The height of it all.
  final double extent;

  bool get isMosaic => groupRows.isNotEmpty;

  Rect rectOf(int i) => Rect.fromLTWH(_rects[4 * i], _rects[4 * i + 1], _rects[4 * i + 2], _rects[4 * i + 3]);

  double _top(int i) => _rects[4 * i + 1];

  /// The photo at height [y], and at [x] across: the last one whose row starts at or above y.
  int at(double y, {double x = 0}) {
    if (count == 0) return 0;
    var lo = 0, hi = count - 1;
    if (_top(0) > y) return 0;
    while (lo < hi) {
      final mid = (lo + hi + 1) ~/ 2;
      if (_top(mid) <= y) {
        lo = mid;
      } else {
        hi = mid - 1;
      }
    }
    // Back along its row, to the photo under x.
    final row = _top(lo);
    var i = lo;
    while (i > 0 && _top(i - 1) == row && _rects[4 * i] > x) {
      i--;
    }
    return i;
  }

  /// Each photo between heights [top] and [bottom], with its place.
  void visit(double top, double bottom, void Function(int i, Rect r) f) {
    if (count == 0) return;
    var i = at(top);
    final row = _top(i);
    while (i > 0 && _top(i - 1) == row) {
      i--;
    }
    // The row above may still reach down into view (its tiles are taller than the gap).
    while (i > 0 && _top(i - 1) + _rects[4 * (i - 1) + 3] >= top) {
      i--;
    }
    for (; i < count && _top(i) <= bottom; i++) {
      final r = rectOf(i);
      if (r.bottom >= top) f(i, r);
    }
  }
}
