// Package entity implements common entity routines.
package entity

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"

	"github.com/oasislabs/oasis-core/go/common/cbor"
	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
	memorySigner "github.com/oasislabs/oasis-core/go/common/crypto/signature/signers/memory"
	"github.com/oasislabs/oasis-core/go/common/prettyprint"
)

const (
	entityFilename = "entity.json"

	fileMode = 0600
)

var (
	testEntity       Entity
	testEntitySigner signature.Signer

	_ prettyprint.PrettyPrinter = (*SignedEntity)(nil)
)

const (
	// LatestEntityDescriptorVersion is the latest entity descriptor version that should be used for
	// all new descriptors. Using earlier versions may be rejected.
	LatestEntityDescriptorVersion = 1

	// Minimum and maximum descriptor versions that are allowed.
	minEntityDescriptorVersion = 0
	maxEntityDescriptorVersion = LatestEntityDescriptorVersion
)

// Entity represents an entity that controls one or more Nodes and or
// services.
type Entity struct { // nolint: maligned
	// DescriptorVersion is the entity descriptor version.
	//
	// It should be bumped whenever breaking changes are made to the descriptor.
	DescriptorVersion uint16 `json:"v,omitempty"`

	// ID is the public key identifying the entity.
	ID signature.PublicKey `json:"id"`

	// Nodes is the vector of node identity keys owned by this entity, that
	// will sign the descriptor with the node signing key rather than the
	// entity signing key.
	Nodes []signature.PublicKey `json:"nodes"`

	// AllowEntitySignedNodes is true iff nodes belonging to this entity
	// may be signed with the entity signing key.
	AllowEntitySignedNodes bool `json:"allow_entity_signed_nodes"`
}

// ValidateBasic performs basic descriptor validity checks.
func (e *Entity) ValidateBasic(strictVersion bool) error {
	switch strictVersion {
	case true:
		// Only the latest version is allowed.
		if e.DescriptorVersion != LatestEntityDescriptorVersion {
			return fmt.Errorf("invalid entity descriptor version (expected: %d got: %d)",
				LatestEntityDescriptorVersion,
				e.DescriptorVersion,
			)
		}
	case false:
		// A range of versions is allowed.
		if e.DescriptorVersion < minEntityDescriptorVersion || e.DescriptorVersion > maxEntityDescriptorVersion {
			return fmt.Errorf("invalid entity descriptor version (min: %d max: %d)",
				minEntityDescriptorVersion,
				maxEntityDescriptorVersion,
			)
		}
	}
	return nil
}

// String returns a string representation of itself.
func (e Entity) String() string {
	return "<Entity id=" + e.ID.String() + ">"
}

// Save saves the JSON serialized entity descriptor.
func (e *Entity) Save(baseDir string) error {
	entityPath := filepath.Join(baseDir, entityFilename)

	// Write to disk.
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(entityPath, b, fileMode)
}

// Load loads an existing entity from disk.
func Load(baseDir string, signerFactory signature.SignerFactory) (*Entity, signature.Signer, error) {
	entityPath := filepath.Join(baseDir, entityFilename)

	// Load the entity signer.
	signer, err := signerFactory.Load(signature.SignerEntity)
	if err != nil {
		return nil, nil, err
	}

	ent, err := LoadDescriptor(entityPath)
	if err != nil {
		signer.Reset()
		return nil, nil, err
	}

	if !ent.ID.Equal(signer.Public()) {
		signer.Reset()
		return nil, nil, fmt.Errorf("public key mismatch (signer: %s, entity: %s)", signer.Public(), ent.ID)
	}

	return ent, signer, nil
}

// LoadDescriptor loads an existing entity from disk, without loading the signer.
// Note: This takes the path to the descriptor rather than a base directory.
func LoadDescriptor(f string) (*Entity, error) {
	rawEnt, err := ioutil.ReadFile(f)
	if err != nil {
		return nil, err
	}

	var ent Entity
	if err = json.Unmarshal(rawEnt, &ent); err != nil {
		return nil, err
	}

	return &ent, nil
}

// GenerateWithSigner generates a new entity using an existing signer and serializes it to disk.
func GenerateWithSigner(baseDir string, signer signature.Signer, template *Entity) (*Entity, error) {
	// Generate a new entity.
	ent := &Entity{
		DescriptorVersion: LatestEntityDescriptorVersion,
		ID:                signer.Public(),
	}
	if template != nil {
		ent.Nodes = template.Nodes
		ent.AllowEntitySignedNodes = template.AllowEntitySignedNodes
	}

	if err := ent.Save(baseDir); err != nil {
		return nil, err
	}
	return ent, nil
}

// Generate generates a new entity and serializes it to disk.
func Generate(baseDir string, signerFactory signature.SignerFactory, template *Entity) (*Entity, signature.Signer, error) {
	// Generate a new entity.
	signer, err := signerFactory.Generate(signature.SignerEntity, rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	ent, err := GenerateWithSigner(baseDir, signer, template)
	if err != nil {
		return nil, nil, err
	}
	return ent, signer, nil
}

// TestEntity returns the built-in test entity and signer.
func TestEntity() (*Entity, signature.Signer, error) {
	return &testEntity, testEntitySigner, nil
}

// SignedEntity is a signed blob containing a CBOR-serialized Entity.
type SignedEntity struct {
	signature.Signed
}

// Open first verifies the blob signature and then unmarshals the blob.
func (s *SignedEntity) Open(context signature.Context, entity *Entity) error { // nolint: interfacer
	return s.Signed.Open(context, entity)
}

// PrettyPrint writes a pretty-printed representation of the type
// to the given writer.
func (s SignedEntity) PrettyPrint(prefix string, w io.Writer) {
	var e Entity
	if err := cbor.Unmarshal(s.Signed.Blob, &e); err != nil {
		fmt.Fprintf(w, "%s<malformed: %s>\n", prefix, err)
		return
	}

	pp := signature.NewPrettySigned(s.Signed, e)
	pp.PrettyPrint(prefix, w)
}

// SignEntity serializes the Entity and signs the result.
func SignEntity(signer signature.Signer, context signature.Context, entity *Entity) (*SignedEntity, error) {
	signed, err := signature.SignSigned(signer, context, entity)
	if err != nil {
		return nil, err
	}

	return &SignedEntity{
		Signed: *signed,
	}, nil
}

func init() {
	testEntitySigner = memorySigner.NewTestSigner("ekiden test entity key seed")

	testEntity.DescriptorVersion = LatestEntityDescriptorVersion
	testEntity.ID = testEntitySigner.Public()
	testEntity.AllowEntitySignedNodes = true
}
