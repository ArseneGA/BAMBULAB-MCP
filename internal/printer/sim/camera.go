package sim

// Caméra simulée d'une P1/A1 : serveur TCP + TLS (même certificat que MQTT/FTPS) qui parle le protocole du port 6000
// (paquet d'authentification de 80 octets, trames de 16 octets d'en-tête + JPEG, voir internal/printer/camera.go).
// Les images sont synthétiques (RenderScene) : un plateau, une pièce dont l'empreinte et la hauteur suivent la couche
// du simulateur, une tête d'impression qui se déplace. Pannes de caméra (CameraSetFault) : spaghetti, pièce décollée,
// caméra hors service. Ce fichier est volontairement séparé de sim.go : il ne le modifie pas.

import (
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Pannes de la caméra simulée.
const (
	CameraFaultSpaghetti = "camera_spaghetti" // des filaments en vrac apparaissent hors de l'empreinte dès la couche 5
	CameraFaultDetached  = "camera_detached"  // la pièce se décolle : l'empreinte se vide
	CameraFaultDown      = "camera_down"      // plus aucune connexion caméra acceptée
)

// CameraConfig configure la caméra simulée.
type CameraConfig struct {
	Port        int           // 0 = éphémère (le vrai : 6000)
	Width       int           // défaut 1280
	Height      int           // défaut 720
	FrameEvery  time.Duration // intervalle entre deux images (défaut 200 ms)
	MaxClients  int           // défaut 2 (la vraie P1 en accepte peu)
	Quality     int           // qualité JPEG (défaut 80)
	SpaghettiAt int           // couche à partir de laquelle CameraFaultSpaghetti agit (défaut 5)
}

// Camera est le serveur caméra simulé.
type Camera struct {
	s    *Sim
	cfg  CameraConfig
	ln   net.Listener
	addr string

	mu      sync.Mutex
	faults  map[string]bool
	clients int
	done    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup

	frames       atomic.Int64
	authFailures atomic.Int64
	busyRefused  atomic.Int64
	conns        atomic.Int64
	sceneFn      func(n int) Scene
}

// StartCamera démarre la caméra simulée de ce simulateur (127.0.0.1 uniquement). À fermer avec Camera.Close.
func (s *Sim) StartCamera(cfg CameraConfig) (*Camera, error) {
	if cfg.Width == 0 {
		cfg.Width = 1280
	}
	if cfg.Height == 0 {
		cfg.Height = 720
	}
	if cfg.FrameEvery == 0 {
		cfg.FrameEvery = 200 * time.Millisecond
	}
	if cfg.MaxClients == 0 {
		cfg.MaxClients = 2
	}
	if cfg.Quality == 0 {
		cfg.Quality = 80
	}
	if cfg.SpaghettiAt == 0 {
		cfg.SpaghettiAt = 5
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, err
	}
	c := &Camera{s: s, cfg: cfg, ln: tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12}),
		addr: ln.Addr().String(), faults: map[string]bool{}, done: make(chan struct{})}
	c.wg.Add(1)
	go c.accept()
	return c, nil
}

// Addr : host:port d'écoute.
func (c *Camera) Addr() string { return c.addr }

// Port : port d'écoute.
func (c *Camera) Port() int {
	_, p, _ := net.SplitHostPort(c.addr)
	n, _ := strconv.Atoi(p)
	return n
}

// Frames : nombre d'images envoyées. AuthFailures : authentifications refusées. BusyRefused : connexions refusées (trop de clients).
func (c *Camera) Frames() int64       { return c.frames.Load() }
func (c *Camera) AuthFailures() int64 { return c.authFailures.Load() }
func (c *Camera) BusyRefused() int64  { return c.busyRefused.Load() }

// Connections : nombre de connexions acceptées et authentifiées depuis le début.
func (c *Camera) Connections() int64 { return c.conns.Load() }

// SetFault active/désactive une panne de caméra.
func (c *Camera) SetFault(name string, on bool) error {
	switch name {
	case CameraFaultSpaghetti, CameraFaultDetached, CameraFaultDown:
	default:
		return fmt.Errorf("sim: unknown camera fault %q", name)
	}
	c.mu.Lock()
	if on {
		c.faults[name] = true
	} else {
		delete(c.faults, name)
	}
	c.mu.Unlock()
	return nil
}

func (c *Camera) fault(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.faults[name]
}

// Close arrête le serveur et coupe les flux.
func (c *Camera) Close() error {
	c.once.Do(func() {
		close(c.done)
		_ = c.ln.Close()
	})
	c.wg.Wait()
	return nil
}

func (c *Camera) accept() {
	defer c.wg.Done()
	for {
		conn, err := c.ln.Accept()
		if err != nil {
			return
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.serve(conn)
		}()
	}
}

func (c *Camera) serve(conn net.Conn) {
	defer conn.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-c.done:
			conn.Close()
		case <-stop:
		}
	}()
	if c.fault(CameraFaultDown) {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var pkt [printer.CameraAuthPacketSize]byte
	if _, err := io.ReadFull(conn, pkt[:]); err != nil {
		return
	}
	user, pass, err := printer.ParseCameraAuthPacket(pkt[:])
	if err != nil || user != "bblp" || subtle.ConstantTimeCompare([]byte(pass), []byte(c.s.cfg.AccessCode)) != 1 {
		c.authFailures.Add(1)
		return
	}
	c.mu.Lock()
	if c.clients >= c.cfg.MaxClients {
		c.mu.Unlock()
		c.busyRefused.Add(1)
		return
	}
	c.clients++
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.clients--; c.mu.Unlock() }()
	c.conns.Add(1)
	_ = conn.SetDeadline(time.Time{})
	t := time.NewTicker(c.cfg.FrameEvery)
	defer t.Stop()
	n := 0
	for {
		jpg, err := c.renderJPEG(n)
		if err != nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(printer.BuildCameraFrame(jpg)); err != nil {
			return
		}
		c.frames.Add(1)
		n++
		select {
		case <-c.done:
			return
		case <-t.C:
		}
	}
}

// SetScene impose la scène rendue (tests du détecteur et du service) ; nil rend la main à l'état du simulateur.
func (c *Camera) SetScene(fn func(n int) Scene) {
	c.mu.Lock()
	c.sceneFn = fn
	c.mu.Unlock()
}

// params traduit l'état du simulateur en paramètres de scène.
func (c *Camera) params(n int) Scene {
	c.mu.Lock()
	fn := c.sceneFn
	c.mu.Unlock()
	if fn != nil {
		sc := fn(n)
		if sc.Width == 0 {
			sc.Width, sc.Height = c.cfg.Width, c.cfg.Height
		}
		if sc.Seed == 0 {
			sc.Seed = int64(n)
		}
		return sc
	}
	c.s.mu.Lock()
	st, layer, total := c.s.p.gcodeState, c.s.p.layer, c.s.p.totalLayers
	c.s.mu.Unlock()
	sc := Scene{Width: c.cfg.Width, Height: c.cfg.Height, Layer: 0, TotalLayers: total, Seed: int64(n)}
	switch st {
	case "RUNNING", "PAUSE":
		sc.Layer = layer
		sc.ObjectPresent = true
		sc.ToolX, sc.ToolY = 0.5+0.35*sinish(n, 7), 0.5+0.3*sinish(n+3, 11)
	case "FINISH":
		sc.Layer = total
		sc.ObjectPresent = true
		sc.ToolX, sc.ToolY = 0.1, 0.1 // tête rangée
	case "PREPARE":
		sc.ToolX, sc.ToolY = 0.5, 0.5
	}
	if c.fault(CameraFaultDetached) && sc.ObjectPresent && layer >= c.cfg.SpaghettiAt {
		sc.ObjectPresent = false
	}
	if c.fault(CameraFaultSpaghetti) && sc.ObjectPresent && layer >= c.cfg.SpaghettiAt {
		sc.Spaghetti = 1
	}
	return sc
}

func (c *Camera) renderJPEG(n int) ([]byte, error) {
	img := RenderScene(c.params(n))
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: c.cfg.Quality}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// sinish : oscillation déterministe sans importer math pour un détail.
func sinish(n, period int) float64 {
	x := float64(n%period) / float64(period)
	if x < 0.5 {
		return 4*x - 1
	}
	return 3 - 4*x
}

// ---- scène synthétique ----

// Scene décrit une image de la caméra : le plateau vu de face, la pièce en cours, la tête, des filaments en vrac.
// Utilisée par la caméra simulée et par les tests du détecteur (images synthétiques).
type Scene struct {
	Width, Height int
	Layer         int
	TotalLayers   int
	ObjectPresent bool    // la pièce est sur le plateau (faux : plateau vide ou pièce décollée)
	Spaghetti     float64 // 0..1 : quantité de filaments en vrac hors de l'empreinte
	ToolX, ToolY  float64 // position de la tête, 0..1 dans la zone du plateau ; négatif = absente
	Seed          int64   // bruit de capteur et tracé des filaments
	Brightness    float64 // 0 = normal ; +0.3 = lumière plus forte (changement d'ambiance)
	ObjectDX      int     // décalage horizontal de la pièce, en pixels (pièce qui glisse)
}

// SceneBed est le rectangle du plateau dans l'image pour une taille donnée.
func SceneBed(w, h int) image.Rectangle {
	return image.Rect(w*3/16, h*2/9, w*13/16, h*11/12)
}

// SceneFootprint est l'empreinte de la pièce au sol (couche 1).
func SceneFootprint(w, h int) image.Rectangle {
	return image.Rect(w*7/16, h*5/9, w*9/16, h*7/9)
}

// RenderScene dessine la scène (RGBA).
func RenderScene(sc Scene) *image.RGBA {
	w, h := sc.Width, sc.Height
	if w == 0 {
		w, h = 1280, 720
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	br := int(sc.Brightness * 255)
	clamp := func(v int) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	fill := func(r image.Rectangle, c color.RGBA) {
		r = r.Intersect(img.Rect)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			o := img.PixOffset(r.Min.X, y)
			for x := r.Min.X; x < r.Max.X; x++ {
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c.R, c.G, c.B, 255
				o += 4
			}
		}
	}
	// mur sombre, plateau gris clair avec quadrillage
	fill(img.Rect, color.RGBA{R: clamp(46 + br), G: clamp(48 + br), B: clamp(54 + br)})
	bed := SceneBed(w, h)
	fill(bed, color.RGBA{R: clamp(176 + br), G: clamp(176 + br), B: clamp(172 + br)})
	for x := bed.Min.X; x < bed.Max.X; x += w / 16 {
		fill(image.Rect(x, bed.Min.Y, x+2, bed.Max.Y), color.RGBA{R: clamp(150 + br), G: clamp(150 + br), B: clamp(148 + br)})
	}
	for y := bed.Min.Y; y < bed.Max.Y; y += h / 9 {
		fill(image.Rect(bed.Min.X, y, bed.Max.X, y+2), color.RGBA{R: clamp(150 + br), G: clamp(150 + br), B: clamp(148 + br)})
	}
	rng := rand.New(rand.NewSource(sc.Seed*7919 + 13))
	// pièce : empreinte + hauteur proportionnelle à la couche (vue de face : la pièce « monte »)
	if sc.ObjectPresent {
		fp := SceneFootprint(w, h).Add(image.Pt(sc.ObjectDX, 0))
		frac := 0.0
		if sc.TotalLayers > 0 {
			frac = float64(sc.Layer) / float64(sc.TotalLayers)
		}
		rise := int(frac * float64(h) * 0.30)
		obj := image.Rect(fp.Min.X, fp.Min.Y-rise, fp.Max.X, fp.Max.Y)
		fill(obj, color.RGBA{R: clamp(226 + br/2), G: clamp(110 + br/2), B: clamp(30 + br/2)})
		fill(image.Rect(obj.Min.X, obj.Min.Y, obj.Max.X, obj.Min.Y+3), color.RGBA{R: 255, G: 170, B: 80, A: 255})
	}
	// filaments en vrac : traits épais clairs et sombres dans le plateau, autour de la pièce
	if sc.Spaghetti > 0 {
		n := int(40 + 120*sc.Spaghetti)
		fp := SceneFootprint(w, h)
		srng := rand.New(rand.NewSource(4242)) // tracé fixe : le désordre est statique d'une image à l'autre
		for i := 0; i < n; i++ {
			x0 := bed.Min.X + srng.Intn(bed.Dx())
			y0 := bed.Min.Y + srng.Intn(bed.Dy())
			x1 := x0 + srng.Intn(w/6) - w/12
			y1 := y0 + srng.Intn(h/5) - h/10
			if image.Pt(x0, y0).In(fp) && image.Pt(x1, y1).In(fp) {
				continue
			}
			col := color.RGBA{R: 232, G: 118, B: 38, A: 255}
			if i%3 == 0 {
				col = color.RGBA{R: 250, G: 214, B: 170, A: 255}
			}
			drawLine(img, x0, y0, x1, y1, 4+srng.Intn(3), col)
		}
	}
	// tête d'impression
	if sc.ToolX >= 0 && sc.ToolY >= 0 {
		tx := bed.Min.X + int(sc.ToolX*float64(bed.Dx()))
		ty := bed.Min.Y + int(sc.ToolY*float64(bed.Dy()))
		fill(image.Rect(tx-w/24, ty-h/24, tx+w/24, ty+h/24), color.RGBA{R: 28, G: 30, B: 34, A: 255})
		fill(image.Rect(tx-w/64, ty+h/24, tx+w/64, ty+h/24+h/30), color.RGBA{R: 200, G: 200, B: 205, A: 255})
	}
	// bruit de capteur : quelques pixels isolés
	for i := 0; i < w*h/400; i++ {
		x, y := rng.Intn(w), rng.Intn(h)
		v := uint8(rng.Intn(40))
		o := img.PixOffset(x, y)
		img.Pix[o], img.Pix[o+1], img.Pix[o+2] = img.Pix[o]/2+v, img.Pix[o+1]/2+v, img.Pix[o+2]/2+v
	}
	return img
}

func drawLine(img *image.RGBA, x0, y0, x1, y1, th int, c color.RGBA) {
	dx, dy := x1-x0, y1-y0
	steps := max(abs(dx), abs(dy))
	if steps == 0 {
		steps = 1
	}
	for i := 0; i <= steps; i++ {
		x, y := x0+dx*i/steps, y0+dy*i/steps
		for yy := y - th/2; yy <= y+th/2; yy++ {
			for xx := x - th/2; xx <= x+th/2; xx++ {
				if image.Pt(xx, yy).In(img.Rect) {
					img.SetRGBA(xx, yy, c)
				}
			}
		}
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// ErrNoCamera est renvoyée quand aucune caméra simulée n'est démarrée.
var ErrNoCamera = errors.New("sim: camera not started")
