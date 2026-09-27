#!/usr/bin/env node
// Prueba de humo para hivescope-relay: confirma, contra un relé real, que las
// políticas de validación de eventos rechazan lo que tienen que rechazar.
//
// Uso:
//   RELAY_URL=wss://relay.hivescope.xyz node test-relay.mjs
//   (por defecto usa ws://localhost:3334, para probar contra un relé local
//   levantado con `docker compose up`)

import WebSocket from 'ws'
import { generateSecretKey, getPublicKey, finalizeEvent } from 'nostr-tools/pure'
import { Relay, useWebSocketImplementation } from 'nostr-tools/relay'

useWebSocketImplementation(WebSocket)

const RELAY_URL = process.env.RELAY_URL || 'ws://localhost:3334'

function nowSeconds() {
	return Math.floor(Date.now() / 1000)
}

// Firma con el formato correcto (65 bytes / 130 caracteres hex, como exige
// hivecrypto.VerifySignature) pero que no fue producida por ninguna clave
// real: sirve para probar el rechazo por "firma inválida" sin necesitar una
// cuenta Hive real.
function fakeHiveSig() {
	return '1f' + 'ab'.repeat(64)
}

// Publica un evento y devuelve { accepted, reason }: nostr-tools resuelve
// relay.publish() con el motivo cuando el relé manda OK true, y la rechaza
// (throw) con el motivo cuando manda OK false.
async function tryPublish(relay, event) {
	try {
		const reason = await relay.publish(event)
		return { accepted: true, reason }
	} catch (err) {
		return { accepted: false, reason: err.message }
	}
}

async function testInvalidHiveLinkIsRejected(relay) {
	console.log('--- Test 1: evento de vinculación (kind 30078, d=hive-link) con firma inválida ---')

	const sk = generateSecretKey()
	const pubkey = getPublicKey(sk)
	console.log(`pubkey de prueba: ${pubkey}`)

	const event = finalizeEvent(
		{
			kind: 30078,
			created_at: nowSeconds(),
			tags: [
				['d', 'hive-link'],
				['hive_account', 'hiveio'],
				['hive_sig', fakeHiveSig()],
				['hive_key_type', 'posting'],
			],
			content: '',
		},
		sk,
	)

	const { accepted, reason } = await tryPublish(relay, event)

	if (accepted) {
		console.error(`FALLO: el relé aceptó una vinculación con firma hive inválida (reason="${reason}")`)
		return false
	}

	console.log(`OK: el relé rechazó el evento -> ${reason}\n`)
	return true
}

async function testChatWithoutLinkIsRejected(relay) {
	console.log('--- Test 2: mensaje de chat (kind 9) de un pubkey sin vinculación previa ---')

	const sk = generateSecretKey()
	const pubkey = getPublicKey(sk)
	console.log(`pubkey de prueba: ${pubkey}`)

	const event = finalizeEvent(
		{
			kind: 9,
			created_at: nowSeconds(),
			tags: [['t', 'sala-general']],
			content: 'hola, esto no debería aceptarse sin vinculación',
		},
		sk,
	)

	const { accepted, reason } = await tryPublish(relay, event)

	if (accepted) {
		console.error(`FALLO: el relé aceptó un mensaje de chat sin vinculación previa (reason="${reason}")`)
		return false
	}

	console.log(`OK: el relé rechazó el evento -> ${reason}\n`)
	return true
}

async function main() {
	console.log(`Conectando a ${RELAY_URL} ...\n`)
	const relay = await Relay.connect(RELAY_URL)

	const results = [
		await testInvalidHiveLinkIsRejected(relay),
		await testChatWithoutLinkIsRejected(relay),
	]

	relay.close()

	const failures = results.filter((ok) => !ok).length
	if (failures > 0) {
		console.error(`${failures} de ${results.length} prueba(s) fallaron.`)
		process.exit(1)
	}

	console.log(`Las ${results.length} pruebas pasaron: el relé rechaza correctamente los eventos inválidos.`)
}

main().catch((err) => {
	console.error('Error inesperado ejecutando las pruebas:', err)
	process.exit(1)
})
