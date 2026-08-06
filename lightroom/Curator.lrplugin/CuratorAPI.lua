local LrHttp = import "LrHttp"
local LrPathUtils = import "LrPathUtils"
local LrPasswords = import "LrPasswords"
local LrLogger = import "LrLogger"

local logger = LrLogger("Curator")
logger:enable("logfile")

local API = {}

local function setting(settings, key, legacyKey)
    local value = settings[key]
    if value == nil or tostring(value) == "" then
        value = settings[legacyKey]
    end
    return value
end

function API.serverURL(settings)
    return tostring(setting(settings, "curatorURL", "LR_curatorURL") or "")
end

function API.token(settings)
    if settings.curatorTokenInput and tostring(settings.curatorTokenInput) ~= "" then
        return tostring(settings.curatorTokenInput)
    end
    local sourceID = tostring(settings.curatorSourceID or "")
    if sourceID ~= "" then
        local ok, token = pcall(LrPasswords.retrieve, "curator-token:" .. sourceID, nil, nil)
        if ok and token and tostring(token) ~= "" then return tostring(token) end
    end
    return tostring(setting(settings, "curatorToken", "LR_curatorToken") or "")
end

function API.storeToken(settings, token)
    local sourceID = tostring(settings.curatorSourceID or "")
    if sourceID == "" then return false, "Curator credential identity is missing" end
    local ok, err = pcall(LrPasswords.store, "curator-token:" .. sourceID, tostring(token), nil, nil)
    if not ok then return false, tostring(err) end
    return true, nil
end

local function endpoint(settings, path)
    return string.gsub(API.serverURL(settings), "/+$", "") .. "/api/v1" .. path
end

local function jsonString(value)
    local encoded = tostring(value or "")
    encoded = string.gsub(encoded, "\\", "\\\\")
    encoded = string.gsub(encoded, '"', '\\"')
    encoded = string.gsub(encoded, "\n", "\\n")
    encoded = string.gsub(encoded, "\r", "\\r")
    return '"' .. encoded .. '"'
end

local function headers(settings)
    return {
        { field = "Authorization", value = "Bearer " .. API.token(settings) },
    }
end

local function transportError(responseHeaders, url)
    local details = responseHeaders and responseHeaders.error
    if type(details) == "table" then
        local parts = {}
        if details.name then table.insert(parts, tostring(details.name)) end
        if details.errorCode then table.insert(parts, "code " .. tostring(details.errorCode)) end
        if details.nativeCode then table.insert(parts, "native " .. tostring(details.nativeCode)) end
        if #parts > 0 then
            return "Could not reach Curator at " .. url .. ": " .. table.concat(parts, ", ")
        end
    elseif details then
        return "Could not reach Curator at " .. url .. ": " .. tostring(details)
    end
    return "Could not reach Curator at " .. url .. ". Check the server URL and that curator serve is running."
end

local function decodeResponse(body, responseHeaders, url)
    if body == nil then
        logger:error(transportError(responseHeaders, url))
        return nil, transportError(responseHeaders, url)
    end
    local status = tonumber(responseHeaders and responseHeaders.status) or 0
    logger:info("HTTP " .. tostring(status) .. " " .. url)
    if status < 200 or status >= 300 then
        local message = string.match(body or "", '"error"%s*:%s*"([^"]*)"') or body
        if status == 0 then
            return nil, transportError(responseHeaders, url)
        end
        return nil, "Curator returned HTTP " .. tostring(status) .. ": " .. tostring(message)
    end
    return {
        name = string.match(body or "", '"name"%s*:%s*"([^"]*)"'),
        version = tonumber(string.match(body or "", '"version"%s*:%s*(%d+)')),
        id = tonumber(string.match(body or "", '"id"%s*:%s*(%d+)')),
        slug = string.match(body or "", '"slug"%s*:%s*"([^"]*)"'),
        url = string.match(body or "", '"url"%s*:%s*"([^"]*)"'),
    }, nil
end

function API.capabilities(settings)
    if API.serverURL(settings) == "" then return nil, "Curator server URL is required" end
    if API.token(settings) == "" then return nil, "Curator publishing token is required" end
    local url = endpoint(settings, "/")
    local body, responseHeaders = LrHttp.get(url, headers(settings), 15)
    local result, err = decodeResponse(body, responseHeaders, url)
    if result and result.version ~= 1 then
        return nil, "Curator Publish API version 1 is required; server reported " .. tostring(result.version or "no version")
    end
    return result, err
end

function API.syncGallery(settings, externalID, title, parentExternalID)
    if API.serverURL(settings) == "" then return nil, "Curator server URL is required" end
    if API.token(settings) == "" then return nil, "Curator publishing token is required" end
    local body = "{" ..
        '"title":' .. jsonString(title) .. "," ..
        '"parent_external_id":' .. jsonString(parentExternalID) .. "}"
    local requestHeaders = headers(settings)
    table.insert(requestHeaders, { field = "Content-Type", value = "application/json" })
    local url = endpoint(settings, "/sync/galleries/" .. tostring(externalID))
    local responseBody, responseHeaders = LrHttp.post(url, body, requestHeaders, "PUT", 30)
    return decodeResponse(responseBody, responseHeaders, url)
end

local function requestWithoutBody(settings, method, path)
    local url = endpoint(settings, path)
    local body, responseHeaders = LrHttp.post(url, "", headers(settings), method, 30)
    return decodeResponse(body, responseHeaders, url)
end

function API.deletePhoto(settings, photoID)
    return requestWithoutBody(settings, "DELETE", "/sync/photos/" .. tostring(photoID))
end

function API.deleteGallery(settings, galleryID)
    return requestWithoutBody(settings, "DELETE", "/sync/galleries/" .. tostring(galleryID))
end

function API.build(settings)
    return requestWithoutBody(settings, "POST", "/sync/build")
end

function API.setPhotoOrder(settings, galleryID, photoIDs)
    local encodedIDs = {}
    for _, photoID in ipairs(photoIDs or {}) do
        table.insert(encodedIDs, tostring(tonumber(photoID)))
    end
    local body = '{"item_ids":[' .. table.concat(encodedIDs, ",") .. "]}"
    local requestHeaders = headers(settings)
    table.insert(requestHeaders, { field = "Content-Type", value = "application/json" })
    local url = endpoint(settings, "/sync/galleries/" .. tostring(galleryID) .. "/order")
    local responseBody, responseHeaders = LrHttp.post(url, body, requestHeaders, "PUT", 30)
    return decodeResponse(responseBody, responseHeaders, url)
end

function API.upload(settings, galleryID, externalID, path, caption, lens, tags, progressCallback)
    if API.serverURL(settings) == "" then return nil, "Curator server URL is required" end
    if API.token(settings) == "" then return nil, "Curator publishing token is required" end
    galleryID = tostring(galleryID or "")
    if galleryID == "" then return nil, "Curator Gallery ID is required" end
    if not string.match(galleryID, "^%d+$") then return nil, "Curator Gallery ID must be a number" end
    local parts = {
        {
            name = "file",
            filePath = path,
            fileName = LrPathUtils.leafName(path),
            contentType = "image/jpeg",
        },
        { name = "caption", value = caption or "" },
        { name = "lens", value = lens or "" },
    }
    for _, tag in ipairs(tags or {}) do
        table.insert(parts, { name = "tag", value = tag })
    end
    local target = "/sync/galleries/" .. galleryID .. "/photos/" .. tostring(externalID)
    local url = endpoint(settings, target)
    local body, responseHeaders = LrHttp.postMultipart(url, parts, headers(settings), 60, progressCallback)
    return decodeResponse(body, responseHeaders, url)
end

return API
