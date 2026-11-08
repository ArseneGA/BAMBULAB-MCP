package agentv2

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// BambuDevice configure l'imprimante Bambu LAN servie par l'agent. Le code d'accès n'existe que dans cette
// structure, côté agent : il n'est jamais envoyé au serveur.
type BambuDevice struct {
	PrinterID  string
	Host       string
	Serial     string
	MQTTPort   int // défaut 8883
	FTPPort    int // défaut 990
	CameraPort int // défaut 6000
	AccessCode string
	PinSHA256  string // empreinte du certificat de l'imprimante (obligatoire : aucune confiance implicite)
	Logger     *slog.Logger
	// Mod ajuste la configuration de l'adaptateur (délais courts en test).
	Mod func(*printer.BambuConfig)
}

// ReadAccessCode lit le code d'accès dans un fichier (une ligne). Le fichier ne doit pas être lisible par d'autres
// utilisateurs (Unix) ; la valeur n'est jamais journalisée.
func ReadAccessCode(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("code d'accès : %w", err)
	}
	code := strings.TrimSpace(string(b))
	if len(code) < 4 || len(code) > 64 {
		return "", errors.New("code d'accès : 4 à 64 caractères attendus dans le fichier")
	}
	return code, nil
}

// Factory renvoie la fabrique d'imprimante et la fonction d'instantané attendues par RunnerConfig.
func (c BambuDevice) Factory() (newDevice func(verify printer.ProofVerifier) (Device, error), snapshot func(ctx context.Context) ([]byte, error), err error) {
	if c.PrinterID == "" || c.Host == "" || c.Serial == "" || c.AccessCode == "" {
		return nil, nil, errors.New("agentv2 : printer-id, hôte, numéro de série et code d'accès requis")
	}
	if c.PinSHA256 == "" {
		return nil, nil, errors.New("agentv2 : empreinte du certificat de l'imprimante requise (--pin-sha256 ; « --discover-pin » la lit pour que vous la compariez)")
	}
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	secret := printer.StaticSecret(c.AccessCode)
	newDevice = func(verify printer.ProofVerifier) (Device, error) {
		cfg := printer.BambuConfig{
			PrinterID: c.PrinterID, Host: c.Host, Serial: c.Serial, MQTTPort: c.MQTTPort, FTPPort: c.FTPPort,
			Secrets: secret, TLS: printer.TLSOptions{PinnedSHA256: c.PinSHA256}, Verifier: verify, Logger: log,
			StartFromTerminal: true, // mêmes règles de départ que le transport direct (FINISH / FAILED acceptés, garde-fous inchangés)
		}
		if c.Mod != nil {
			c.Mod(&cfg)
		}
		b, err := printer.NewBambu(cfg)
		if err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := b.Connect(cctx); err != nil {
			_ = b.Close()
			return nil, fmt.Errorf("connexion à l'imprimante : %w", err)
		}
		return b, nil
	}
	cam, err := printer.NewCameraClient(printer.CameraConfig{Host: c.Host, Port: c.CameraPort, Secrets: secret, TLS: printer.TLSOptions{PinnedSHA256: c.PinSHA256}, Logger: log})
	if err != nil {
		return nil, nil, err
	}
	snapshot = func(ctx context.Context) ([]byte, error) {
		b, err := cam.Snapshot(ctx)
		if errors.Is(err, printer.ErrCameraAuth) { // client précédent pas encore libéré côté caméra : un nouvel essai
			time.Sleep(500 * time.Millisecond)
			b, err = cam.Snapshot(ctx)
		}
		return b, err
	}
	return newDevice, snapshot, nil
}

// PairAgent échange le jeton d'appairage (mécanisme existant) et renvoie la configuration à enregistrer. Si
// expectKeyID est renseigné, la clé du serveur reçue doit avoir cet identifiant (comparaison hors bande).
func PairAgent(ctx context.Context, cl *Client, serverURL, fingerprint, token, name, host, printerID, version, expectKeyID string) (*LinkConfig, error) {
	resp, err := cl.Pair(ctx, PairRequest{Token: token, Name: name, Host: host, AgentVersion: version, PrinterID: printerID})
	if err != nil {
		return nil, err
	}
	pub, err := (&LinkConfig{ServerKey: resp.ServerKey}).ServerPublicKey()
	if err != nil {
		return nil, err
	}
	if KeyID(ed25519.PublicKey(pub)) != resp.ServerKeyID {
		return nil, errors.New("agentv2 : identifiant de clé incohérent dans la réponse du serveur")
	}
	if expectKeyID != "" && !strings.EqualFold(expectKeyID, resp.ServerKeyID) {
		return nil, fmt.Errorf("agentv2 : la clé du serveur (%s) n'est pas celle attendue (%s) : appairage refusé", resp.ServerKeyID, expectKeyID)
	}
	return &LinkConfig{ServerURL: strings.TrimRight(serverURL, "/"), ServerFingerprint: fingerprint, AgentID: resp.AgentID, Secret: resp.Secret,
		ServerKey: resp.ServerKey, ServerKeyID: resp.ServerKeyID, PrinterID: printerID, Name: name}, nil
}
