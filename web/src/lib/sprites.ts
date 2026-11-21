/**
 * The drawn pixel sprites a teammate can wear as its avatar, stored as `sprite:<name>`. Each is
 * drawn in up to 12 by 12 pixels and centred on a 16-pixel grid, so it is sharp at whole
 * multiples of 16 px. A letter is a colour from the palette (or the sprite's own colours) and
 * "." is empty; outline: true adds a dark edge around the shape. The server accepts the same
 * names (teammates.AvatarSprites), in this order.
 */

const palette: Record<string, string> = {
  k: "#1f1b18", // outline, eyes
  o: "#e8853a", // orange
  w: "#f4efe6", // white
  p: "#f29bb0", // pink
  P: "#d9708c", // dark pink
  b: "#8a5a35", // brown
  t: "#d9b27c", // tan
  y: "#f2c641", // yellow
  g: "#5aa84a", // green
  v: "#3f7d33", // dark green
  e: "#8fd14f", // lime
  s: "#f1c39b", // light skin
  S: "#a8693f", // dark skin
  h: "#3a2a20", // dark hair
  H: "#c4572e", // red hair
  c: "#3f74c9", // blue
  m: "#a8473a", // mouth
  r: "#d6453d", // red
  n: "#b8613b", // terracotta
  q: "#9aa3a8", // grey
  u: "#efe2c6", // cream
  d: "#3b4650", // slate
  l: "#a68be0", // lilac
  i: "#bfe3f2", // ice
  a: "#4fb3c8", // teal
  f: "#f6d7a7", // sand
}

type Sprite = {
  label: string
  outline?: boolean
  colors?: Record<string, string>
  rows: string[]
}

const sprites = {
  cat: {
    label: "Cat",
    rows: [
      ".k........k.",
      ".kk......kk.",
      ".kok....kok.",
      ".kookkkkook.",
      "kooooooooook",
      "koekooookeok",
      "kooooooooook",
      "kooooppooook",
      ".kooooooook.",
      "..kooooook..",
      "...kkkkkk...",
    ],
  },
  fox: {
    label: "Fox",
    rows: [
      "k..........k",
      "kk........kk",
      "kok......kok",
      "kook....kook",
      "kooookkooook",
      "kookooookook",
      "kwoooooooowk",
      ".kwwoooowwk.",
      "..kwwwwwwk..",
      "...kwkkwk...",
      "....kkkk....",
    ],
  },
  owl: {
    label: "Owl",
    rows: [
      "............",
      ".kk......kk.",
      ".kbk....kbk.",
      ".kbbkkkkbbk.",
      "kbbbbbbbbbbk",
      "kbwwwbbwwwbk",
      "kbwkwbbwkwbk",
      "kbwwwbbwwwbk",
      "kbbbbyybbbbk",
      "kbttttttttbk",
      ".kbttttttbk.",
      "..kkkkkkkk..",
    ],
  },
  frog: {
    label: "Frog",
    rows: [
      "............",
      ".kkk....kkk.",
      "kwwwk..kwwwk",
      "kwkwkkkkwkwk",
      "kggggggggggk",
      "kggggggggggk",
      "kgkggggggkgk",
      "kggkkkkkkggk",
      "kggggggggggk",
      ".kggggggggk.",
      "..kkkkkkkk..",
    ],
  },
  rabbit: {
    label: "Rabbit",
    outline: true,
    rows: [
      ".ww......ww.",
      ".wp......pw.",
      ".wp......pw.",
      ".wp......pw.",
      ".www....www.",
      ".wwwwwwwwww.",
      "wwwwwwwwwwww",
      "wwkwwwwwwkww",
      "wwwwwppwwwww",
      "wpwwwwwwwwpw",
      ".wwwwwwwwww.",
      "..wwwwwwww..",
    ],
  },
  bear: {
    label: "Bear",
    outline: true,
    rows: [
      "............",
      ".bb......bb.",
      "btbbbbbbbbtb",
      "bbbbbbbbbbbb",
      "bbbbbbbbbbbb",
      "bbkbbbbbbkbb",
      "bbbbttttbbbb",
      "bbbtkkkktbbb",
      "bbbttttttbbb",
      ".bbbbbbbbbb.",
      "..bbbbbbbb..",
    ],
  },
  panda: {
    label: "Panda",
    outline: true,
    rows: [
      "............",
      ".kk......kk.",
      "kkkwwwwwwkkk",
      ".wwwwwwwwww.",
      "wwwwwwwwwwww",
      "wkkwwwwwwkkw",
      "wkkwwwwwwkkw",
      "wwwwwkkwwwww",
      "wwwwwwwwwwww",
      ".wwwwwwwwww.",
      "..wwwwwwww..",
    ],
  },
  penguin: {
    label: "Penguin",
    outline: true,
    rows: [
      "............",
      "...dddddd...",
      "..dddddddd..",
      ".ddwwwwwwdd.",
      ".dwkwwwwkwd.",
      ".dwwwoowwwd.",
      "ddwwwwwwwwdd",
      "ddwwwwwwwwdd",
      "ddwwwwwwwwdd",
      ".dwwwwwwwwd.",
      ".ddwwwwwwdd.",
      "..oo....oo..",
    ],
  },
  pig: {
    label: "Pig",
    outline: true,
    rows: [
      "............",
      ".pp......pp.",
      "pPppppppppPp",
      "pppppppppppp",
      "pppppppppppp",
      "ppkppppppkpp",
      "pppPPPPPPppp",
      "pppPkPPkPppp",
      "pppPPPPPPppp",
      ".pppppppppp.",
      "..pppppppp..",
    ],
  },
  mouse: {
    label: "Mouse",
    outline: true,
    rows: [
      ".qq......qq.",
      "qppq....qppq",
      "qppq....qppq",
      "qqqqqqqqqqqq",
      ".qqqqqqqqqq.",
      ".qkqqqqqqkq.",
      "..qqqqqqqq..",
      "..qqqppqqq..",
      "...qqqqqq...",
      "....qqqq....",
    ],
  },
  chick: {
    label: "Chick",
    outline: true,
    rows: [
      "............",
      "....yyyy....",
      "...yyyyyy...",
      "..yyyyyyyy..",
      ".yyyyyyyyyy.",
      ".ykyyyyyyky.",
      "yyyyyooyyyyy",
      "yyyyyyyyyyyy",
      "yyyyyyyyyyyy",
      ".yyyyyyyyyy.",
      "..yyyyyyyy..",
      "...o....o...",
    ],
  },
  duck: {
    label: "Duck",
    outline: true,
    rows: [
      "............",
      "....yyy.....",
      "...yyyyy....",
      "...yykyy....",
      "...yyyyyoo..",
      "....yyy.....",
      ".y..yyyy....",
      ".yyyyyyyyy..",
      ".yyyyyyyyy..",
      "..yyyyyyyy..",
      "...yyyyyy...",
    ],
  },
  octopus: {
    label: "Octopus",
    outline: true,
    rows: [
      "............",
      "...llllll...",
      "..llllllll..",
      ".llllllllll.",
      ".llllllllll.",
      ".lkllllllkl.",
      ".llllllllll.",
      ".llllllllll.",
      "llllllllllll",
      "l.ll.ll.ll.l",
      "l..l....l..l",
    ],
  },
  ghost: {
    label: "Ghost",
    outline: true,
    rows: [
      "............",
      "...wwwwww...",
      "..wwwwwwww..",
      ".wwwwwwwwww.",
      ".wwkwwwwkww.",
      ".wwkwwwwkww.",
      ".wwwwwwwwww.",
      ".wwwwkkwwww.",
      ".wwwwwwwwww.",
      ".wwwwwwwwww.",
      ".w.ww..ww.w.",
    ],
  },
  slime: {
    label: "Slime",
    outline: true,
    rows: [
      "............",
      "............",
      "....eeee....",
      "...eeeeee...",
      "..eieeeeie..",
      ".eieeeeeeie.",
      ".eekeeeekee.",
      "eeekeeeekeee",
      "eeeeeeeeeeee",
      "eeeeevveeeee",
      ".eeeeeeeeee.",
      "..vvvvvvvv..",
    ],
  },
  robot: {
    label: "Robot",
    outline: true,
    rows: [
      ".....rr.....",
      ".....dd.....",
      ".qqqqqqqqqq.",
      "dqqqqqqqqqqd",
      "dqccqqqqccqd",
      "dqccqqqqccqd",
      "dqqqqqqqqqqd",
      "dqqdqddqdqqd",
      ".qqqqqqqqqq.",
      "..dddddddd..",
      ".cccccccccc.",
      ".cccccccccc.",
    ],
  },
  rocket: {
    label: "Rocket",
    outline: true,
    rows: [
      ".....rr.....",
      "....rrrr....",
      "....wwww....",
      "...wwwwww...",
      "...wccccw...",
      "...wccccw...",
      "...wwwwww...",
      "...wwwwww...",
      "..rwwwwwwr..",
      ".rrwwwwwwrr.",
      ".r..oooo..r.",
      ".....yy.....",
    ],
  },
  tree: {
    label: "Tree",
    outline: true,
    rows: [
      "....gggg....",
      "..gggggggg..",
      ".gggggggggg.",
      ".ggvggggvgg.",
      "gggggggggggg",
      "gvggggggggvg",
      "gggggvvggggg",
      ".gggggggggg.",
      "..gggggggg..",
      "....bbbb....",
      "....bbbb....",
      "...bbbbbb...",
    ],
  },
  flower: {
    label: "Flower",
    outline: true,
    rows: [
      "...yy..yy...",
      "..yyyyyyyy..",
      ".yyybbbbyyy.",
      ".yybbbbbbyy.",
      ".yybbbbbbyy.",
      ".yyybbbbyyy.",
      "..yyyyyyyy..",
      "...yy..yy...",
      ".....gg.....",
      "..gg.gg.gg..",
      "...gggggg...",
      ".....gg.....",
    ],
  },
  cactus: {
    label: "Cactus",
    outline: true,
    rows: [
      ".....gg.....",
      "....gggg....",
      "....gggg.g..",
      ".g..gvgg.gg.",
      ".gg.gggg.gg.",
      ".gg.ggggggg.",
      ".ggggggg....",
      "....gvgg....",
      "....gggg....",
      "..nnnnnnnn..",
      "..nnnnnnnn..",
      "...nnnnnn...",
    ],
  },
  mushroom: {
    label: "Mushroom",
    outline: true,
    rows: [
      "............",
      "...rrrrrr...",
      ".rrwrrrrwrr.",
      "rrrrrrrrrrrr",
      "rwrrrrrrrrwr",
      "rrrrrwwrrrrr",
      "..uuuuuuuu..",
      "....uuuu....",
      "....uuuu....",
      "...uuuuuu...",
    ],
  },
  mug: {
    label: "Mug",
    rows: [
      "............",
      "...q..q.....",
      "....q..q....",
      "...q..q.....",
      ".kkkkkkkk...",
      ".kwwwwwwkkk.",
      ".kwwrrwwk.k.",
      ".kwrrrrwkkk.",
      ".kwwrrwwk...",
      ".kwwwwwwk...",
      "..kkkkkk....",
    ],
  },
  "dark-hair": {
    label: "Dark hair",
    outline: true,
    rows: [
      "..hhhhhhhh..",
      ".hhhhhhhhhh.",
      "hhhhhhhhhhhh",
      "hhsssssssshh",
      "hssssssssssh",
      "hssksssskssh",
      "ssssssssssss",
      ".ssssssssss.",
      ".ssssmmssss.",
      "..ssssssss..",
      "...ssssss...",
      "..cccccccc..",
    ],
  },
  "red-hair": {
    label: "Red hair",
    outline: true,
    rows: [
      "..HHHHHHHH..",
      ".HHHHHHHHHH.",
      "HHHHHHHHHHHH",
      "HHHssssssHHH",
      "HHssssssssHH",
      "HHskssssksHH",
      "HHssssssssHH",
      "HHsssmmsssHH",
      "HHHssssssHHH",
      "HH.cccccc.HH",
      "HH.cccccc.HH",
    ],
  },
  cap: {
    label: "Cap",
    outline: true,
    rows: [
      "............",
      "..cccccccc..",
      ".cccccccccc.",
      "cccccccccccc",
      ".ssssssssss.",
      ".skkkkkkkks.",
      ".skksssskks.",
      ".ssssssssss.",
      ".bssssssssb.",
      ".bbbbmmbbbb.",
      "..bbbbbbbb..",
      "...bbbbbb...",
    ],
  },
  curls: {
    label: "Curls",
    outline: true,
    colors: { c: "#d0a03a" },
    rows: [
      "..hhhhhhhh..",
      ".hhhhhhhhhh.",
      "hhhhhhhhhhhh",
      "hhhhhhhhhhhh",
      "hhSSSSSSSShh",
      "hhSkSSSSkShh",
      "hhSSSSSSSShh",
      ".hSSSmmSSSh.",
      "..SSSSSSSS..",
      "...SSSSSS...",
      "..cccccccc..",
      ".cccccccccc.",
    ],
  },
  shades: {
    label: "Sunglasses",
    outline: true,
    rows: [
      "...bbbbbb...",
      ".bbbbbbbbbb.",
      "bbbbbbbbbbbb",
      "bssssssssssb",
      "skkkkkkkkkks",
      "skkkksskkkks",
      "ssssssssssss",
      ".ssssssssss.",
      ".ssssmmssss.",
      "..ssssssss..",
      "...ssssss...",
      "..gggggggg..",
    ],
  },
  beard: {
    label: "Beard",
    outline: true,
    rows: [
      "............",
      "..hhhhhhhh..",
      ".hhhhhhhhhh.",
      ".hssssssssh.",
      ".ssssssssss.",
      ".skssssssks.",
      ".ssssssssss.",
      ".hhsssssshh.",
      ".hhhhmmhhhh.",
      "..hhhhhhhh..",
      "...hhhhhh...",
      "..rrrrrrrr..",
    ],
  },
  bun: {
    label: "Bun",
    outline: true,
    colors: { c: "#a68be0" },
    rows: [
      "....yyyy....",
      "...yyyyyy...",
      "..yyyyyyyy..",
      ".yyyyyyyyyy.",
      "yyssssssssyy",
      "yssssssssssy",
      "yssksssskssy",
      "ssssssssssss",
      ".ssssmmssss.",
      "..ssssssss..",
      "...ssssss...",
      "..cccccccc..",
    ],
  },
  headphones: {
    label: "Headphones",
    outline: true,
    rows: [
      "...dddddd...",
      "..dhhhhhhd..",
      ".dhhhhhhhhd.",
      ".dhhhhhhhhd.",
      "rrSSSSSSSSrr",
      "rrSkSSSSkSrr",
      "rrSSSSSSSSrr",
      ".SSSSmmSSSS.",
      "..SSSSSSSS..",
      "...SSSSSS...",
      "..aaaaaaaa..",
      ".aaaaaaaaaa.",
    ],
  },
} satisfies Record<string, Sprite>

export type SpriteName = keyof typeof sprites

/** The sprites' names, in the order the avatar picker shows them. */
export const spriteNames = Object.keys(sprites) as SpriteName[]

export function isSpriteName(name: string): name is SpriteName {
  return Object.hasOwn(sprites, name)
}

/** The sprite's name for people, such as "Fox" or "Headphones". */
export function spriteLabel(name: SpriteName) {
  return sprites[name].label
}

/**
 * The sprite a teammate without a chosen avatar wears, picked from its name. The hash differs
 * from the colour's, so two teammates of one colour still tend to get different sprites.
 */
export function spriteFor(name: string): SpriteName {
  let h = 2166136261
  for (const c of name) h = Math.imul(h ^ (c.codePointAt(0) ?? 0), 16777619)
  return spriteNames[(h >>> 0) % spriteNames.length]
}

const drawn = new Map<SpriteName, { fill: string; d: string }[]>()

/** The sprite as one path per colour on the 16-unit grid, merging runs along each row. */
export function spritePaths(name: SpriteName) {
  const done = drawn.get(name)
  if (done) return done
  const sprite: Sprite = sprites[name]
  const grid: (string | undefined)[][] = Array.from({ length: 16 }, () =>
    Array<string | undefined>(16).fill(undefined)
  )
  const top = 2 + Math.floor((12 - sprite.rows.length) / 2)
  sprite.rows.forEach((row, y) =>
    Array.from(row).forEach((ch, x) => {
      if (ch !== ".") grid[y + top][x + 2] = sprite.colors?.[ch] ?? palette[ch]
    })
  )
  if (sprite.outline) {
    const filled = (y: number, x: number) => !!grid[y]?.[x]
    const edges: [number, number][] = []
    for (let y = 0; y < 16; y++)
      for (let x = 0; x < 16; x++)
        if (
          !grid[y][x] &&
          (filled(y - 1, x) ||
            filled(y + 1, x) ||
            filled(y, x - 1) ||
            filled(y, x + 1))
        )
          edges.push([y, x])
    for (const [y, x] of edges) grid[y][x] = palette.k
  }
  const paths = new Map<string, string>()
  for (let y = 0; y < 16; y++)
    for (let x = 0; x < 16;) {
      const fill = grid[y][x]
      let end = x + 1
      while (fill && end < 16 && grid[y][end] === fill) end++
      if (fill)
        paths.set(
          fill,
          `${paths.get(fill) ?? ""}M${x} ${y}h${end - x}v1h${x - end}z`
        )
      x = end
    }
  const out = [...paths].map(([fill, d]) => ({ fill, d }))
  drawn.set(name, out)
  return out
}
