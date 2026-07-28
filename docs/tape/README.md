# Las grabaciones de terminal

Dos `.tape` de [VHS](https://github.com/charmbracelet/vhs), la herramienta de charm que graba
GIF/MP4/WebM de una terminal desde un guion. batten ya usa bubbletea y lipgloss, así que es el
mismo ecosistema.

| archivo | qué graba | salida |
|---|---|---|
| [`demo.tape`](demo.tape) | `batten demo`: el flujo entero — commit denegado, el check que corre y falla, el fix, el commit que entra | `docs/img/demo.gif` |
| [`tui.tape`](tui.tape) | revisar una corrida en la UI de terminal | `docs/img/tui.gif` |

## Regenerar

```sh
vhs docs/tape/demo.tape
vhs docs/tape/tui.tape
```

**Requiere `vhs`, y VHS a su vez requiere `ttyd` y `ffmpeg`.** No están instalados en el equipo
donde se escribieron estos guiones, así que **los `.tape` están verificados en su contenido —los
comandos que ejecutan se corrieron a mano y producen la salida que describen— pero los GIF no se
generaron todavía.**

```sh
# macOS
brew install vhs
# Linux (o ver las instrucciones del repo de vhs)
go install github.com/charmbracelet/vhs@latest && apt install ttyd ffmpeg
```

## Por qué un `.tape` y no un GIF grabado a mano

Una grabación de pantalla es una afirmación sobre el software que **deja de ser verificable en el
momento en que el software cambia**. Un `.tape` es código: se vuelve a correr y el GIF se
regenera con lo que batten hace hoy. Si la salida cambia, el GIF cambia. Lo que no puede es
envejecer en silencio y seguir pareciendo convincente — que es exactamente lo que le pasa a casi
todos los GIF de README.

## Dos decisiones que hay que respetar al editarlos

**`demo.tape` no arma nada.** Corre `batten demo`, que se construye su propio repo y lo borra.
Sin setup, sin tocar nada del que mira. Si alguna vez hace falta preparar el terreno, va entre
`Hide`/`Show`: el setup no es la historia.

**`tui.tape` exporta `BATTEN_DB` antes de abrir la TUI.** Sin eso, `dbPath()` cae a la base real
del usuario y una grabación hecha para un README estaría mostrando el trabajo de alguien. Por eso
existe `batten demo --dir`: deja el sandbox en una ruta **conocida**, que un guion puede escribir
de antemano. (`--dir` se niega a escribir en un directorio que no creó él, así que no puede
pisarte nada.)
