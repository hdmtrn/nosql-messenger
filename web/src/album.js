// How several pictures of one message share a rectangle. Ported from Telegram
// Desktop's Ui::Layouter (ui/grouped_layout.cpp): the shapes of two, three and
// four pictures are named cases, and everything else is a search over ways to
// cut the set into rows. The geometry below follows theirs step for step, so
// their file is the reference for anything not explained here.
//
// The result is in the coordinates of a MAX-wide box. The template turns it
// into percentages, which is what makes the album shrink with the bubble
// without measuring anything.

// Telegram works with a 430px album, a 100px floor and a 4px gap. Ours is the
// 280px picture box the single-picture path already uses, with the floor scaled
// to match, since it is a fraction of the album in their code as well.
const MAX = 280
const MIN = 65
const GAP = 4

// Wider than 1.2 is "w", taller than 0.8 is "n", the rest is "q". The cases
// below are chosen by this string, exactly as CountProportions does.
const shape = (ratio) => (ratio > 1.2 ? 'w' : ratio < 0.8 ? 'n' : 'q')

const round = (v) => Math.round(v)
const rect = (x, y, w, h) => ({ x: round(x), y: round(y), w: round(w), h: round(h) })

function two(ratios, proportions, averageRatio) {
  const maxHeight = MAX
  // Two wide pictures of nearly the same shape stand one above the other; the
  // maxSizeRatio is 1 for a square box, which is the box we ask for.
  if (proportions === 'ww' && averageRatio > 1.4 && ratios[1] - ratios[0] < 0.2) {
    const h = Math.min(MAX / ratios[0], MAX / ratios[1], (maxHeight - GAP) / 2)
    return [rect(0, 0, MAX, h), rect(0, h + GAP, MAX, h)]
  }
  if (proportions === 'ww' || proportions === 'qq') {
    const w = (MAX - GAP) / 2
    const h = Math.min(w / ratios[0], w / ratios[1], maxHeight)
    return [rect(0, 0, w, h), rect(w + GAP, 0, w, h)]
  }
  // Otherwise they keep their own proportions side by side, and the second one
  // is never allowed below 1.5 floors.
  const secondWidth = Math.min(
    Math.max(0.4 * (MAX - GAP), (MAX - GAP) / ratios[0] / (1 / ratios[0] + 1 / ratios[1])),
    MAX - GAP - MIN * 1.5
  )
  const firstWidth = MAX - secondWidth - GAP
  const h = Math.min(maxHeight, Math.min(firstWidth / ratios[0], secondWidth / ratios[1]))
  return [rect(0, 0, firstWidth, h), rect(firstWidth + GAP, 0, secondWidth, h)]
}

function three(ratios, proportions) {
  const maxHeight = MAX
  // A tall first picture stands on the left with the other two beside it;
  // anything else puts the first one on top with the two under it.
  if (proportions[0] === 'n') {
    const firstHeight = maxHeight
    const thirdHeight = Math.min(
      (maxHeight - GAP) / 2,
      (ratios[1] * (MAX - GAP)) / (ratios[2] + ratios[1])
    )
    const secondHeight = firstHeight - thirdHeight - GAP
    const rightWidth = Math.max(
      MIN,
      Math.min((MAX - GAP) / 2, thirdHeight * ratios[2], secondHeight * ratios[1])
    )
    const leftWidth = Math.min(firstHeight * ratios[0], MAX - GAP - rightWidth)
    return [
      rect(0, 0, leftWidth, firstHeight),
      rect(leftWidth + GAP, 0, rightWidth, secondHeight),
      rect(leftWidth + GAP, secondHeight + GAP, rightWidth, thirdHeight),
    ]
  }
  const firstHeight = Math.min(MAX / ratios[0], (maxHeight - GAP) * 0.66)
  const secondWidth = (MAX - GAP) / 2
  const secondHeight = Math.min(
    maxHeight - firstHeight - GAP,
    Math.min(secondWidth / ratios[1], secondWidth / ratios[2])
  )
  return [
    rect(0, 0, MAX, firstHeight),
    rect(0, firstHeight + GAP, secondWidth, secondHeight),
    rect(secondWidth + GAP, firstHeight + GAP, MAX - secondWidth - GAP, secondHeight),
  ]
}

function four(ratios, proportions) {
  const maxHeight = MAX
  // A wide first picture takes the top row and the other three sit under it;
  // otherwise it takes the left column and they stack beside it.
  if (proportions[0] === 'w') {
    const h0 = Math.min(MAX / ratios[0], (maxHeight - GAP) * 0.66)
    const h = (MAX - 2 * GAP) / (ratios[1] + ratios[2] + ratios[3])
    const w0 = Math.max(MIN, Math.min((MAX - 2 * GAP) * 0.4, h * ratios[1]))
    const w2 = Math.max(MIN, (MAX - 2 * GAP) * 0.33, h * ratios[3])
    const w1 = MAX - w0 - w2 - 2 * GAP
    const h1 = Math.min(maxHeight - h0 - GAP, h)
    return [
      rect(0, 0, MAX, h0),
      rect(0, h0 + GAP, w0, h1),
      rect(w0 + GAP, h0 + GAP, w1, h1),
      rect(w0 + GAP + w1 + GAP, h0 + GAP, w2, h1),
    ]
  }
  const h = maxHeight
  const w0 = Math.min(h * ratios[0], (MAX - GAP) * 0.6)
  const w = (maxHeight - 2 * GAP) / (1 / ratios[1] + 1 / ratios[2] + 1 / ratios[3])
  const h0 = w / ratios[1]
  const h1 = w / ratios[2]
  const h2 = h - h0 - h1 - 2 * GAP
  const w1 = Math.max(MIN, Math.min(MAX - w0 - GAP, w))
  return [
    rect(0, 0, w0, h),
    rect(w0 + GAP, 0, w1, h0),
    rect(w0 + GAP, h0 + GAP, w1, h1),
    rect(w0 + GAP, h0 + h1 + 2 * GAP, w1, h2),
  ]
}

// Five and up, and any set holding a picture wider than 2:1. Every way of
// cutting the sequence into two, three or four rows of at most three is tried;
// a row's height follows from the widths it has to fit, and the winner is the
// one whose total height lands closest to the box. Two penalties push away
// from rows thinner than the floor and from a wide row above a narrow one.
function complex(ratios, averageRatio) {
  const maxHeight = (MAX * 4) / 3
  // A picture too far from the others is cropped rather than allowed to set
  // the height of a whole row.
  const cropped = ratios.map((r) =>
    averageRatio > 1.1 ? Math.min(Math.max(r, 1), 2.75) : Math.min(Math.max(r, 0.6667), 1)
  )
  const count = cropped.length

  const rowHeight = (offset, n) => {
    let sum = 0
    for (let i = offset; i < offset + n; i++) sum += cropped[i]
    return (MAX - (n - 1) * GAP) / sum
  }

  const attempts = []
  const push = (counts) => {
    const heights = []
    let offset = 0
    for (const n of counts) {
      heights.push(rowHeight(offset, n))
      offset += n
    }
    attempts.push({ counts, heights })
  }

  for (let first = 1; first < count; first++) {
    const second = count - first
    if (first <= 3 && second <= 3) push([first, second])
  }
  for (let first = 1; first < count - 1; first++) {
    for (let second = 1; second < count - first; second++) {
      const third = count - first - second
      if (first <= 3 && second <= (averageRatio < 0.85 ? 4 : 3) && third <= 3) {
        push([first, second, third])
      }
    }
  }
  for (let first = 1; first < count - 1; first++) {
    for (let second = 1; second < count - first; second++) {
      for (let third = 1; third < count - first - second; third++) {
        const fourth = count - first - second - third
        if (first <= 3 && second <= 3 && third <= 3 && fourth <= 3) {
          push([first, second, third, fourth])
        }
      }
    }
  }

  let best = null
  let bestDiff = 0
  for (const attempt of attempts) {
    const total = attempt.heights.reduce((a, b) => a + b, 0) + GAP * (attempt.counts.length - 1)
    const thin = Math.min(...attempt.heights) < MIN ? 1.5 : 1
    let widening = 1
    for (let line = 1; line < attempt.counts.length; line++) {
      if (attempt.counts[line - 1] > attempt.counts[line]) widening = 1.5
    }
    const diff = Math.abs(total - maxHeight) * thin * widening
    if (!best || diff < bestDiff) {
      best = attempt
      bestDiff = diff
    }
  }

  const tiles = []
  let index = 0
  let y = 0
  for (let row = 0; row < best.counts.length; row++) {
    const cols = best.counts[row]
    const height = best.heights[row]
    let x = 0
    for (let col = 0; col < cols; col++) {
      // The last one takes what is left, so rounding never shows as a seam.
      const width = col === cols - 1 ? MAX - x : round(cropped[index] * height)
      tiles.push(rect(x, y, width, height))
      x += width + GAP
      index++
    }
    // The row's height is rounded once and the next row starts below it, as it
    // does in Telegram: rounding per row, not per tile, keeps the seams straight.
    y += round(height) + GAP
  }
  return tiles
}

// attachments: [{ width, height }] as the server stores them. Returns the box
// and a tile per attachment, in the same order, plus which corners of the album
// each tile owns — only those are rounded, as in a real album.
export function albumLayout(attachments) {
  const ratios = attachments.map((a) => (a.width && a.height ? a.width / a.height : 1))
  const proportions = ratios.map(shape).join('')
  const averageRatio = ratios.reduce((a, b) => a + b, 0) / ratios.length

  let tiles
  if (ratios.length >= 5 || ratios.some((r) => r > 2)) {
    tiles = complex(ratios, averageRatio)
  } else if (ratios.length === 2) {
    tiles = two(ratios, proportions, averageRatio)
  } else if (ratios.length === 3) {
    tiles = three(ratios, proportions)
  } else {
    tiles = four(ratios, proportions)
  }

  const width = Math.max(...tiles.map((t) => t.x + t.w))
  const height = Math.max(...tiles.map((t) => t.y + t.h))
  // A corner of a tile is a corner of the album when it sits on two of its
  // edges — the same rule as Telegram's GetCornersFromSides, read off the
  // geometry instead of carried along. The pixel of slack is for the rounding.
  const near = (a, b) => Math.abs(a - b) <= 1
  for (const t of tiles) {
    const left = near(t.x, 0)
    const top = near(t.y, 0)
    const right = near(t.x + t.w, width)
    const bottom = near(t.y + t.h, height)
    t.corners = [left && top, right && top, right && bottom, left && bottom]
  }
  return { width, height, tiles }
}
