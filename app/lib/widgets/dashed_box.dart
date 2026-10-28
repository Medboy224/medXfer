/// A rounded box drawn with a dashed outline: where files are dropped (mockup « 10. Bureau »).
library;

import 'dart:ui' show PathMetric;

import 'package:flutter/material.dart';

class DashedBox extends StatelessWidget {
  const DashedBox({super.key, required this.color, required this.child, this.radius = 20, this.width = 2, this.fill});

  final Color color;
  final Color? fill;
  final double radius;
  final double width;
  final Widget child;

  @override
  Widget build(BuildContext context) => CustomPaint(
    painter: _Dashes(color: color, radius: radius, width: width, fill: fill),
    child: child,
  );
}

class _Dashes extends CustomPainter {
  _Dashes({required this.color, required this.radius, required this.width, this.fill});

  final Color color;
  final Color? fill;
  final double radius;
  final double width;

  static const _dash = 8.0;
  static const _gap = 6.0;

  @override
  void paint(Canvas canvas, Size size) {
    final box = RRect.fromRectAndRadius(Offset.zero & size, Radius.circular(radius)).deflate(width / 2);
    if (fill != null) canvas.drawRRect(box, Paint()..color = fill!);
    final pen = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = width;
    for (final PathMetric m in (Path()..addRRect(box)).computeMetrics()) {
      for (var d = 0.0; d < m.length; d += _dash + _gap) {
        canvas.drawPath(m.extractPath(d, d + _dash), pen);
      }
    }
  }

  @override
  bool shouldRepaint(_Dashes old) => old.color != color || old.fill != fill || old.radius != radius || old.width != width;
}
