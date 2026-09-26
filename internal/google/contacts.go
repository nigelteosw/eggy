package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// contactFields is the projection every People call shares. The API requires
// it explicitly on reads and on writes, and asking for the same three things
// everywhere is what keeps a Contact meaning one thing.
const contactFields = "names,emailAddresses,phoneNumbers"

type Contact struct {
	// Resource is People's own identifier ("people/c123"). It is the only
	// handle an update can use, so a list that omitted it would be read-only in
	// practice.
	Resource string   `json:"resource,omitempty"`
	Name     string   `json:"name,omitempty"`
	Emails   []string `json:"emails,omitempty"`
	Phones   []string `json:"phones,omitempty"`
}

type person struct {
	ResourceName string `json:"resourceName"`
	ETag         string `json:"etag"`
	Names        []struct {
		DisplayName string `json:"displayName"`
		GivenName   string `json:"givenName"`
	} `json:"names"`
	EmailAddresses []struct {
		Value string `json:"value"`
	} `json:"emailAddresses"`
	PhoneNumbers []struct {
		Value string `json:"value"`
	} `json:"phoneNumbers"`
}

func (p person) contact() Contact {
	contact := Contact{Resource: p.ResourceName}
	if len(p.Names) > 0 {
		contact.Name = p.Names[0].DisplayName
		if contact.Name == "" {
			contact.Name = p.Names[0].GivenName
		}
	}
	for _, email := range p.EmailAddresses {
		contact.Emails = append(contact.Emails, email.Value)
	}
	for _, phone := range p.PhoneNumbers {
		contact.Phones = append(contact.Phones, phone.Value)
	}
	return contact
}

// ContactsList is the capability the MCP path cannot reach at all: Google
// hosts no People MCP server, so this is the one product where the choice of
// integration decides whether it exists.
func (w *Workspace) ContactsList(ctx context.Context, max int) ([]Contact, error) {
	if max <= 0 || max > 100 {
		max = 20
	}
	values := url.Values{"pageSize": {fmt.Sprint(max)}, "personFields": {contactFields}}
	var response struct {
		Connections []person `json:"connections"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.People+"/people/me/connections", values, nil, &response); err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(response.Connections))
	for _, found := range response.Connections {
		contacts = append(contacts, found.contact())
	}
	return contacts, nil
}

// warmupWindow is how long one cache warm is trusted for. Google requires a
// warmup before searching but says nothing about how long it lasts; a minute
// keeps a burst of searches to one extra request without holding a stale cache
// across a conversation.
const warmupWindow = time.Minute

// searchWarmup serializes the warmup so several searches in one turn do not
// each pay for it. It is process state, not durable state: a restart warming
// up again costs one request.
type searchWarmup struct {
	mu   sync.Mutex
	last time.Time
}

// ContactsSearch finds a contact by name, email or phone prefix, which is what
// "what is Sam's number" actually needs -- listing 20 of several hundred
// contacts and hoping answers nothing.
//
// The warmup request is Google's requirement, not a precaution: searchContacts
// reads a server-side cache, and without a warm one the first search of a
// session returns stale results or none. It costs a second request, so it is
// spent once a minute rather than once a search.
func (w *Workspace) ContactsSearch(ctx context.Context, query string, max int) ([]Contact, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("a search query is required")
	}
	if max <= 0 || max > 30 {
		max = 10
	}
	if err := w.warmContactSearch(ctx); err != nil {
		return nil, err
	}
	values := url.Values{"query": {query}, "pageSize": {fmt.Sprint(max)}, "readMask": {contactFields}}
	var response struct {
		Results []struct {
			Person person `json:"person"`
		} `json:"results"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.People+"/people:searchContacts", values, nil, &response); err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0, len(response.Results))
	for _, result := range response.Results {
		contacts = append(contacts, result.Person.contact())
	}
	return contacts, nil
}

func (w *Workspace) warmContactSearch(ctx context.Context) error {
	w.warmup.mu.Lock()
	defer w.warmup.mu.Unlock()
	if time.Since(w.warmup.last) < warmupWindow {
		return nil
	}
	values := url.Values{"query": {""}, "readMask": {contactFields}}
	if err := w.call(ctx, http.MethodGet, w.endpoints.People+"/people:searchContacts", values, nil, nil); err != nil {
		return err
	}
	w.warmup.last = time.Now()
	return nil
}

// ContactsCreate saves a new contact.
func (w *Workspace) ContactsCreate(ctx context.Context, contact Contact) (Contact, error) {
	if strings.TrimSpace(contact.Name) == "" && len(contact.Emails) == 0 && len(contact.Phones) == 0 {
		return Contact{}, errors.New("a contact needs at least a name, an email or a phone number")
	}
	values := url.Values{"personFields": {contactFields}}
	var created person
	if err := w.call(ctx, http.MethodPost, w.endpoints.People+"/people:createContact", values, contactBody(contact), &created); err != nil {
		return Contact{}, err
	}
	return created.contact(), nil
}

// ContactsUpdate replaces the fields it is given on an existing contact.
//
// People requires the current etag on every update, and the etag has to come
// from a fresh read: it is how the API refuses to let one edit silently
// overwrite another made since. updatePersonFields then names exactly which
// fields are being replaced -- anything named and not supplied is cleared, so
// only the fields the caller actually set are listed.
func (w *Workspace) ContactsUpdate(ctx context.Context, resource string, contact Contact) (Contact, error) {
	if strings.TrimSpace(resource) == "" {
		return Contact{}, errors.New("a contact resource name is required; action=search returns it")
	}
	var current person
	read := url.Values{"personFields": {contactFields}}
	if err := w.call(ctx, http.MethodGet, w.endpoints.People+"/"+resource, read, nil, &current); err != nil {
		return Contact{}, err
	}
	body := contactBody(contact)
	body["etag"] = current.ETag
	fields := make([]string, 0, 3)
	if strings.TrimSpace(contact.Name) != "" {
		fields = append(fields, "names")
	}
	if len(contact.Emails) > 0 {
		fields = append(fields, "emailAddresses")
	}
	if len(contact.Phones) > 0 {
		fields = append(fields, "phoneNumbers")
	}
	if len(fields) == 0 {
		return Contact{}, errors.New("nothing to change: give a name, emails or phones")
	}
	values := url.Values{"updatePersonFields": {strings.Join(fields, ",")}, "personFields": {contactFields}}
	var updated person
	endpoint := w.endpoints.People + "/" + resource + ":updateContact"
	if err := w.call(ctx, http.MethodPatch, endpoint, values, body, &updated); err != nil {
		return Contact{}, err
	}
	return updated.contact(), nil
}

// contactBody shapes People's repeated-value fields. Every field is a list of
// objects even when there is one of them, and a display name is not writable:
// People derives it from the given name.
func contactBody(contact Contact) map[string]any {
	body := map[string]any{}
	if name := strings.TrimSpace(contact.Name); name != "" {
		given, family, split := strings.Cut(name, " ")
		entry := map[string]any{"givenName": given}
		if split {
			entry["familyName"] = family
		}
		body["names"] = []map[string]any{entry}
	}
	if len(contact.Emails) > 0 {
		emails := make([]map[string]any, 0, len(contact.Emails))
		for _, email := range contact.Emails {
			if trimmed := strings.TrimSpace(email); trimmed != "" {
				emails = append(emails, map[string]any{"value": trimmed})
			}
		}
		body["emailAddresses"] = emails
	}
	if len(contact.Phones) > 0 {
		phones := make([]map[string]any, 0, len(contact.Phones))
		for _, phone := range contact.Phones {
			if trimmed := strings.TrimSpace(phone); trimmed != "" {
				phones = append(phones, map[string]any{"value": trimmed})
			}
		}
		body["phoneNumbers"] = phones
	}
	return body
}
