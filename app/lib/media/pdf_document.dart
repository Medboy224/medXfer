/// The PDF reader's documents (plan J8, WP-8.2, mockups 27 and 28 of 24 November 2026): what
/// draws a PDF's pages, behind an interface. On Android, the system's own renderer (PdfRenderer),
/// nothing added to the APK; none elsewhere yet (the desktops come with WP-8.2b).
library;

import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/services.dart';

import '../platform/folders.dart' show platformChannel;

/// What opens PDFs.
abstract interface class PdfReader {
  bool get supported;

  /// Opens [path]; throws [PdfProtected] for a PDF locked by a password, [PdfFailed] otherwise.
  Future<PdfDocument> open(String path);
}

/// One open PDF: its pages' sizes, each drawn on demand. Closed once the reader goes.
abstract interface class PdfDocument {
  /// Each page's size, in points.
  List<ui.Size> get pages;

  /// Page [index] drawn [width] px wide; or, given [region] (in the pixels of that width), only
  /// that part of it. Null once closed.
  Future<ui.Image?> render(int index, int width, {ui.Rect? region});

  void close();
}

class PdfProtected implements Exception {
  const PdfProtected();
}

class PdfFailed implements Exception {
  const PdfFailed(this.detail);
  final String detail;
  @override
  String toString() => detail;
}

class NoPdfReader implements PdfReader {
  const NoPdfReader();

  @override
  bool get supported => false;

  @override
  Future<PdfDocument> open(String path) async => throw const PdfFailed('no PDF reader on this system');
}

/// MainActivity.kt and PdfDocs.kt: the pages come as RGBA pixels.
class AndroidPdfReader implements PdfReader {
  const AndroidPdfReader();

  @override
  bool get supported => true;

  @override
  Future<PdfDocument> open(String path) async {
    final Map<Object?, Object?>? reply;
    try {
      reply = await platformChannel.invokeMapMethod<Object?, Object?>('pdfOpen', {'path': path});
    } on PlatformException catch (e) {
      if (e.code == 'protected') throw const PdfProtected();
      throw PdfFailed(e.message ?? e.code);
    }
    if (reply == null) throw const PdfFailed('no reply');
    final sizes = (reply['sizes'] as List).cast<num>();
    return _AndroidPdf((reply['id'] as num).toInt(), [
      for (var i = 0; i + 1 < sizes.length; i += 2) ui.Size(sizes[i].toDouble(), sizes[i + 1].toDouble()),
    ]);
  }
}

class _AndroidPdf implements PdfDocument {
  _AndroidPdf(this.id, this.pages);

  final int id;
  @override
  final List<ui.Size> pages;
  bool _closed = false;

  @override
  Future<ui.Image?> render(int index, int width, {ui.Rect? region}) async {
    if (_closed) return null;
    final Map<Object?, Object?>? reply;
    try {
      reply = await platformChannel.invokeMapMethod<Object?, Object?>('pdfRender', {
        'id': id,
        'page': index,
        'width': width,
        if (region != null) ...{'left': region.left.round(), 'top': region.top.round(), 'w': region.width.round(), 'h': region.height.round()},
      });
    } on PlatformException {
      return null;
    }
    if (reply == null || _closed) return null;
    return rgbaImage(reply['px'] as Uint8List, (reply['w'] as num).toInt(), (reply['h'] as num).toInt());
  }

  @override
  void close() {
    if (_closed) return;
    _closed = true;
    platformChannel.invokeMethod<void>('pdfClose', {'id': id}).ignore();
  }
}

/// RGBA pixels made an image, decoded away from the thread that draws.
Future<ui.Image> rgbaImage(Uint8List pixels, int width, int height) async {
  final buffer = await ui.ImmutableBuffer.fromUint8List(pixels);
  final descriptor = ui.ImageDescriptor.raw(buffer, width: width, height: height, pixelFormat: ui.PixelFormat.rgba8888);
  final codec = await descriptor.instantiateCodec();
  try {
    return (await codec.getNextFrame()).image;
  } finally {
    codec.dispose();
    descriptor.dispose();
    buffer.dispose();
  }
}

PdfReader systemPdfReader() => Platform.isAndroid ? const AndroidPdfReader() : const NoPdfReader();
