// Pictures are re-encoded in the browser before upload, as Telegram Desktop does:
// the server stores what it gets, so this is where size and metadata are decided.
// createImageBitmap applies the EXIF rotation and reads formats the server
// refuses, such as WebP and HEIC; drawing on a canvas drops the EXIF itself.

// Telegram's default: 1280 px on the long side, JPEG quality 87.
export const PHOTO_SIDE = 1280
const QUALITY = 0.87

function encode(canvas) {
  return new Promise((resolve, reject) =>
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('could not encode the image'))),
      'image/jpeg', QUALITY))
}

// Fits the picture into side x side, or cuts its centre square to exactly that
// when square is set. Transparent areas come out white, as JPEG has no alpha.
export async function toJpeg(file, { side, square = false }) {
  const bitmap = await createImageBitmap(file)
  const { width, height } = bitmap
  const crop = square ? Math.min(width, height) : null
  const scale = Math.min(1, side / (crop ?? Math.max(width, height)))
  const outW = square ? side : Math.round(width * scale)
  const outH = square ? side : Math.round(height * scale)

  const canvas = document.createElement('canvas')
  canvas.width = outW
  canvas.height = outH
  const ctx = canvas.getContext('2d')
  ctx.fillStyle = '#fff'
  ctx.fillRect(0, 0, outW, outH)
  if (square) {
    ctx.drawImage(bitmap, (width - crop) / 2, (height - crop) / 2, crop, crop, 0, 0, outW, outH)
  } else {
    ctx.drawImage(bitmap, 0, 0, outW, outH)
  }
  bitmap.close()
  return encode(canvas)
}

// What goes to the server for a picture in a message. An animated GIF would lose
// every frame but the first on a canvas, so it goes as it is.
export function prepareForSending(file) {
  if (file.type === 'image/gif') return Promise.resolve(file)
  return toJpeg(file, { side: PHOTO_SIDE })
}
