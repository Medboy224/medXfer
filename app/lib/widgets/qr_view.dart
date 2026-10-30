/// A QR code on screen. The `qr` package (pure Dart, BSD) encodes; this widget is its only
/// user: another encoder would replace this file alone.
library;

import 'package:flutter/material.dart';
import 'package:qr/qr.dart';

class QrView extends StatelessWidget {
  const QrView({super.key, required this.data, this.size = 220});

  final String data;
  final double size;

  @override
  Widget build(BuildContext context) {
    // Dark on white whatever the theme: the cameras read it best (and the quiet zone around).
    // Correction M (15 % of the modules), not L (7 %): a code on a screen, found by the reader but
    // misread, failed its checksum on 2261 pictures in a row (real test of 30 October 2026,
    // #227). 53 modules rather than 49 for a pairing link with its network.
    final image = QrImage(QrCode(payload: QrPayload.fromString(data), errorCorrectLevel: QrErrorCorrectLevel.medium));
    return Semantics(
      label: 'QR',
      image: true,
      child: Container(
        width: size,
        height: size,
        padding: EdgeInsets.all(size / 16),
        decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(16)),
        child: RepaintBoundary(child: CustomPaint(painter: _QrPainter(data, image), size: Size.infinite)),
      ),
    );
  }
}

class _QrPainter extends CustomPainter {
  _QrPainter(this.data, this.image);

  final String data;
  final QrImage image;

  @override
  void paint(Canvas canvas, Size size) {
    final n = image.moduleCount;
    final cell = size.shortestSide / n;
    final path = Path();
    for (var y = 0; y < n; y++) {
      for (var x = 0; x < n; x++) {
        // A hair wider than a cell: no seam between neighbours.
        if (image.isDark(y, x)) path.addRect(Rect.fromLTWH(x * cell, y * cell, cell + 0.3, cell + 0.3));
      }
    }
    canvas.drawPath(path, Paint()..color = const Color(0xFF0B0D10));
  }

  @override
  bool shouldRepaint(_QrPainter old) => old.data != data;
}
